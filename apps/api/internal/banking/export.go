package banking

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strings"
	"time"

	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
)

// ------------------------------------------------------------------------------
// CSV
// ------------------------------------------------------------------------------

// ExportCSV genera el historial en formato CSV.
//
// Las cabeceras van en español porque este archivo lo abre una persona en Excel
// o en Google Sheets, no una máquina.
//
// Antes de la tabla se incluye un bloque de resumen —cuenta, fecha de emisión y
// totales—. Un CSV que arranca directamente en las filas obliga a quien lo
// recibe a calcular a mano lo que el sistema ya sabe.
func ExportCSV(transactions []domain.Transaction, accountNumber string) ([]byte, error) {
	var buf bytes.Buffer

	// BOM UTF-8. Sin esto Excel en Windows interpreta el archivo como ANSI y
	// muestra "HernÃ¡ndez" en lugar de "Hernández". Los datos de prueba están
	// llenos de acentos, así que importa.
	buf.Write([]byte{0xEF, 0xBB, 0xBF})

	w := csv.NewWriter(&buf)

	summary := summarize(transactions)

	// Bloque de encabezado. Las celdas vacías mantienen la rejilla alineada
	// cuando la planilla abre el archivo.
	preamble := [][]string{
		{"Banca AI - Historial de movimientos"},
		{"Cuenta", accountNumber},
		{"Generado", time.Now().Format("2006-01-02 15:04:05")},
		{"Movimientos", fmt.Sprintf("%d", len(transactions))},
		{"Total entradas", summary.inflow.String()},
		{"Total salidas", summary.outflow.String()},
		{"Resultado neto", summary.net()},
		{},
	}

	for _, row := range preamble {
		if err := w.Write(row); err != nil {
			return nil, fmt.Errorf("no se pudo escribir el resumen del CSV: %w", err)
		}
	}

	header := []string{"Fecha", "Hora", "Tipo", "Descripción", "Origen", "Destino", "Monto", "Moneda", "Estado"}
	if err := w.Write(header); err != nil {
		return nil, fmt.Errorf("no se pudo escribir la cabecera del CSV: %w", err)
	}

	for _, tx := range transactions {
		// El signo indica la dirección: negativo si el dinero sale de esta
		// cuenta. Así la columna se puede sumar directamente en la planilla.
		amount := tx.Amount.String()
		if tx.Direction == domain.DirectionOut {
			amount = "-" + amount
		}

		// Fecha y hora se separan en dos columnas: así la planilla puede
		// ordenar y filtrar por día sin tener que partir el texto.
		row := []string{
			tx.Timestamp.Format("2006-01-02"),
			tx.Timestamp.Format("15:04:05"),
			translateType(tx.Type),
			tx.Description,
			tx.FromAccount,
			tx.ToAccount,
			amount,
			tx.Currency,
			translateStatus(tx.Status),
		}

		if err := w.Write(row); err != nil {
			return nil, fmt.Errorf("no se pudo escribir una fila del CSV: %w", err)
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("falló la generación del CSV: %w", err)
	}

	return buf.Bytes(), nil
}

// totals acumula entradas y salidas para el resumen.
type totals struct {
	inflow  domain.Money
	outflow domain.Money
}

// net devuelve el resultado del período, con signo.
func (t totals) net() string {
	diff := t.inflow - t.outflow
	if diff < 0 {
		return "-" + (-diff).String()
	}
	return diff.String()
}

func summarize(transactions []domain.Transaction) totals {
	var t totals
	for _, tx := range transactions {
		if tx.Direction == domain.DirectionOut {
			t.outflow += tx.Amount
			continue
		}
		t.inflow += tx.Amount
	}
	return t
}

// ------------------------------------------------------------------------------
// PDF
//
// Se construye a mano en lugar de sumar una librería: el documento es una tabla,
// y el formato PDF permite armarlo con primitivas básicas. Evita una dependencia
// de varios megabytes para una función secundaria.
// ------------------------------------------------------------------------------

// Geometría del documento, en puntos (1 pt = 1/72"). Una hoja A4 son 595x842.
const (
	pageWidth  = 595
	pageHeight = 842
	marginX    = 40

	rowHeight    = 18
	headerHeight = 22
	bottomMargin = 56
)

// Columnas de la tabla: posición horizontal y ancho disponible para el texto.
//
// Se declaran juntas para que el ancho de recorte y la posición no se
// desincronicen: un texto más largo que su columna se solapa con la siguiente,
// y en un PDF no hay nada que lo impida.
var pdfColumns = struct {
	date, kind, description, amount int
}{
	date:        marginX + 4,
	kind:        marginX + 90,
	description: marginX + 190,
	// Los montos se alinean a la derecha, así que ésta es su posición final.
	amount: pageWidth - marginX - 6,
}

// ExportPDF genera el historial como un PDF paginado.
func ExportPDF(transactions []domain.Transaction, accountNumber string, generatedAt time.Time) ([]byte, error) {
	summary := summarize(transactions)

	var pages []string
	var current bytes.Buffer

	y := drawFirstPageHeader(&current, accountNumber, generatedAt, len(transactions), summary)
	y = drawTableHeader(&current, y)

	rowIndex := 0
	for _, tx := range transactions {
		if y < bottomMargin {
			pages = append(pages, current.String())
			current.Reset()

			y = pageHeight - 56
			y = drawTableHeader(&current, y)
			rowIndex = 0
		}

		drawRow(&current, y, tx, rowIndex)
		y -= rowHeight
		rowIndex++
	}

	if len(transactions) == 0 {
		drawText(&current, marginX+4, y-4, 10, fontRegular, colorMuted,
			"No hay movimientos en este período.")
	}

	pages = append(pages, current.String())

	return buildPDF(pages)
}

// drawFirstPageHeader dibuja el encabezado con el resumen y devuelve la
// coordenada vertical donde puede empezar la tabla.
func drawFirstPageHeader(buf *bytes.Buffer, accountNumber string, generatedAt time.Time, count int, summary totals) int {
	// Banda superior en el violeta de marca: da identidad al documento y separa
	// el encabezado del contenido sin necesidad de una línea.
	fillRect(buf, 0, pageHeight-70, pageWidth, 70, colorBrand)

	drawText(buf, marginX, pageHeight-42, 18, fontBold, colorOnBrand, "Banca AI")
	drawText(buf, marginX, pageHeight-60, 10, fontRegular, colorOnBrandSoft, "Historial de movimientos")

	// Cuenta y fecha alineadas a la derecha, dentro de la misma banda.
	drawTextRight(buf, pageWidth-marginX, pageHeight-42, 10, fontBold, colorOnBrand, accountNumber)
	drawTextRight(buf, pageWidth-marginX, pageHeight-60, 9, fontRegular, colorOnBrandSoft,
		"Generado: "+generatedAt.Format("2006-01-02 15:04"))

	y := pageHeight - 96

	// Tarjetas de resumen. Tres cifras que responden lo primero que se pregunta
	// quien abre un estado de cuenta: cuánto entró, cuánto salió, cómo cerró.
	cardWidth := (pageWidth - marginX*2 - 16) / 3
	cards := []struct {
		label string
		value string
		color [3]float64
	}{
		{"Entradas", summary.inflow.String(), colorPositive},
		{"Salidas", summary.outflow.String(), colorNegative},
		{"Resultado neto", summary.net(), colorInk},
	}

	for i, card := range cards {
		x := marginX + i*(cardWidth+8)

		fillRect(buf, x, y-44, cardWidth, 44, colorSurface)
		drawText(buf, x+10, y-16, 8, fontRegular, colorMuted, strings.ToUpper(card.label))
		drawText(buf, x+10, y-34, 13, fontBold, card.color, card.value)
	}

	y -= 62

	drawText(buf, marginX, y, 9, fontRegular, colorMuted,
		fmt.Sprintf("%d movimientos en el período", count))

	return y - 18
}

// drawTableHeader dibuja la fila de títulos y devuelve la altura de la primera
// fila de datos.
func drawTableHeader(buf *bytes.Buffer, y int) int {
	fillRect(buf, marginX, y-headerHeight+6, pageWidth-marginX*2, headerHeight, colorHeaderBg)

	textY := y - headerHeight + 13

	drawText(buf, pdfColumns.date, textY, 8, fontBold, colorMuted, "FECHA")
	drawText(buf, pdfColumns.kind, textY, 8, fontBold, colorMuted, "TIPO")
	drawText(buf, pdfColumns.description, textY, 8, fontBold, colorMuted, "CONCEPTO")
	drawTextRight(buf, pdfColumns.amount, textY, 8, fontBold, colorMuted, "MONTO")

	return y - headerHeight - 6
}

// drawRow dibuja un movimiento.
//
// Las filas pares llevan un fondo muy tenue: en una tabla larga sin separación
// horizontal la vista salta de renglón al recorrerla.
func drawRow(buf *bytes.Buffer, y int, tx domain.Transaction, index int) {
	if index%2 == 1 {
		fillRect(buf, marginX, y-5, pageWidth-marginX*2, rowHeight, colorZebra)
	}

	amount := tx.Amount.String()
	amountColor := colorPositive
	if tx.Direction == domain.DirectionOut {
		amount = "-" + amount
		amountColor = colorInk
	} else {
		amount = "+" + amount
	}

	drawText(buf, pdfColumns.date, y, 9, fontRegular, colorSecondary,
		tx.Timestamp.Format("2006-01-02"))
	drawText(buf, pdfColumns.kind, y, 9, fontRegular, colorInk,
		truncate(translateType(tx.Type), 16))
	drawText(buf, pdfColumns.description, y, 9, fontRegular, colorSecondary,
		truncate(descriptionOr(tx), 34))

	// El monto va en negrita y alineado a la derecha: es el dato que se busca al
	// recorrer la columna, y alineado se compara de un vistazo.
	drawTextRight(buf, pdfColumns.amount, y, 9, fontBold, amountColor, amount)
}

// descriptionOr usa el tipo de operación cuando no hay concepto, para que la
// columna nunca quede vacía.
func descriptionOr(tx domain.Transaction) string {
	if tx.Description != "" {
		return tx.Description
	}
	return translateType(tx.Type)
}

// ------------------------------------------------------------------------------
// Primitivas de dibujo
// ------------------------------------------------------------------------------

const (
	fontRegular = "F1"
	fontBold    = "F2"
)

// Paleta del documento, en RGB normalizado (0..1), que es como lo espera PDF.
// Se corresponde con el sistema de diseño de la aplicación.
var (
	colorBrand       = [3]float64{0.278, 0.231, 0.941} // violet-600 #473BF0
	colorOnBrand     = [3]float64{1, 1, 1}
	colorOnBrandSoft = [3]float64{0.85, 0.84, 0.98}
	colorInk         = [3]float64{0.0, 0.02, 0.0} // ink #000500
	colorSecondary   = [3]float64{0.29, 0.29, 0.33}
	colorMuted       = [3]float64{0.44, 0.44, 0.49}
	colorSurface     = [3]float64{0.949, 0.949, 0.969}
	colorHeaderBg    = [3]float64{0.92, 0.92, 0.95}
	colorZebra       = [3]float64{0.975, 0.975, 0.985}
	colorPositive    = [3]float64{0.0, 0.537, 0.482} // success #00897B
	colorNegative    = [3]float64{0.702, 0.149, 0.118}
)

// fillRect pinta un rectángulo sólido.
//
// q/Q aíslan el estado gráfico: sin ellos el color de relleno se filtraría al
// resto del contenido de la página.
func fillRect(buf *bytes.Buffer, x, y, w, h int, color [3]float64) {
	fmt.Fprintf(buf, "q\n%.3f %.3f %.3f rg\n%d %d %d %d re\nf\nQ\n",
		color[0], color[1], color[2], x, y, w, h)
}

// drawText escribe texto alineado a la izquierda.
func drawText(buf *bytes.Buffer, x, y, size int, font string, color [3]float64, text string) {
	fmt.Fprintf(buf, "BT\n%.3f %.3f %.3f rg\n/%s %d Tf\n%d %d Td\n(%s) Tj\nET\n",
		color[0], color[1], color[2], font, size, x, y, pdfEscape(text))
}

// drawTextRight escribe texto terminado en x.
//
// PDF no sabe alinear a la derecha: hay que estimar el ancho del texto y correr
// el origen hacia la izquierda. El factor 0.5 aproxima el ancho medio de
// Helvetica respecto al tamaño de fuente, suficiente para una columna de montos.
func drawTextRight(buf *bytes.Buffer, x, y, size int, font string, color [3]float64, text string) {
	width := int(float64(len([]rune(text))) * float64(size) * 0.5)
	drawText(buf, x-width, y, size, font, color, text)
}

// buildPDF arma el documento con sus objetos y la tabla de referencias cruzadas
// que el formato exige.
//
// La numeración de objetos es la parte delicada: el catálogo y el árbol de
// páginas son 1 y 2, las fuentes 3 y 4, y a partir de ahí cada página ocupa dos
// objetos (la página y su contenido).
func buildPDF(pages []string) ([]byte, error) {
	if len(pages) == 0 {
		pages = []string{""}
	}

	const (
		catalogID = 1
		pagesID   = 2
		fontsID   = 3 // F1; F2 es fontsID+1
	)
	firstPageID := fontsID + 2

	// Referencias a los objetos de página, para el árbol de páginas.
	var kids strings.Builder
	for i := range pages {
		fmt.Fprintf(&kids, "%d 0 R ", firstPageID+i*2)
	}

	objects := make([]string, 0, 4+len(pages)*2)

	objects = append(objects,
		fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pagesID),
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>",
			strings.TrimSpace(kids.String()), len(pages)),
		// Las fuentes base están en todos los lectores de PDF: no hace falta
		// incrustarlas y el archivo queda mucho más liviano.
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>",
	)

	for i, content := range pages {
		// El pie va acá y no en el dibujo de cada página porque recién ahora se
		// conoce el total.
		var withFooter bytes.Buffer
		withFooter.WriteString(content)
		drawText(&withFooter, marginX, 32, 8, fontRegular, colorMuted,
			fmt.Sprintf("Banca AI · Página %d de %d", i+1, len(pages)))
		drawTextRight(&withFooter, pageWidth-marginX, 32, 8, fontRegular, colorMuted,
			"Documento generado automáticamente")

		body := withFooter.String()
		contentID := firstPageID + i*2 + 1

		objects = append(objects,
			fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %d %d] "+
				"/Resources << /Font << /%s %d 0 R /%s %d 0 R >> >> /Contents %d 0 R >>",
				pagesID, pageWidth, pageHeight,
				fontRegular, fontsID, fontBold, fontsID+1, contentID),
			fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(body), body),
		)
	}

	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.4\n")

	// La tabla xref necesita el desplazamiento en bytes de cada objeto, así que
	// se van registrando a medida que se escriben.
	offsets := make([]int, len(objects))
	for i, obj := range objects {
		offsets[i] = pdf.Len()
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}

	xrefOffset := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n", len(objects)+1)
	pdf.WriteString("0000000000 65535 f \n")
	for _, offset := range offsets {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offset)
	}

	fmt.Fprintf(&pdf, "trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objects)+1, catalogID, xrefOffset)

	return pdf.Bytes(), nil
}

// pdfEscape protege los caracteres con significado especial dentro de una
// cadena PDF, y convierte los acentos a WinAnsi.
func pdfEscape(s string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`(`, `\(`,
		`)`, `\)`,
	)
	s = replacer.Replace(s)

	// Las fuentes base usan WinAnsiEncoding (un byte por carácter), así que los
	// caracteres UTF-8 de varios bytes hay que convertirlos.
	var out strings.Builder
	for _, r := range s {
		if r < 128 {
			out.WriteRune(r)
			continue
		}
		if code, ok := winAnsi[r]; ok {
			fmt.Fprintf(&out, `\%03o`, code)
			continue
		}
		out.WriteRune('?')
	}

	return out.String()
}

// winAnsi mapea los caracteres acentuados del español a WinAnsiEncoding.
var winAnsi = map[rune]int{
	'á': 0341, 'é': 0351, 'í': 0355, 'ó': 0363, 'ú': 0372,
	'Á': 0301, 'É': 0311, 'Í': 0315, 'Ó': 0323, 'Ú': 0332,
	'ñ': 0361, 'Ñ': 0321,
	'ü': 0374, 'Ü': 0334,
	'¿': 0277, '¡': 0241,
	'°': 0260, '€': 0200,
	'·': 0267,
}

// translateType convierte el tipo de transacción a su nombre en español.
func translateType(t domain.TransactionType) string {
	switch t {
	case domain.TransactionTypeDeposit:
		return "Depósito"
	case domain.TransactionTypeWithdrawal:
		return "Retiro"
	case domain.TransactionTypeTransfer:
		return "Transferencia"
	case domain.TransactionTypeInternalTransfer:
		return "Entre cuentas"
	default:
		return string(t)
	}
}

// translateStatus convierte el estado a su nombre en español.
func translateStatus(s domain.TransactionStatus) string {
	switch s {
	case domain.TransactionStatusPending:
		return "Pendiente"
	case domain.TransactionStatusCompleted:
		return "Completada"
	case domain.TransactionStatusVoided:
		return "Cancelada"
	default:
		return string(s)
	}
}

// truncate acorta un texto agregando puntos suspensivos.
// Trabaja sobre runas y no sobre bytes, para no partir un carácter acentuado
// por la mitad.
func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return string(runes[:max-3]) + "..."
}
