package banking

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strings"
	"time"

	"github.com/Moisinho/banca-ai/apps/api/internal/domain"
)

// ExportCSV genera el historial en formato CSV.
//
// Las cabeceras van en español porque este archivo lo abre una persona en Excel
// o en Google Sheets, no una máquina.
func ExportCSV(transactions []domain.Transaction, accountNumber string) ([]byte, error) {
	var buf bytes.Buffer

	// BOM UTF-8. Sin esto Excel en Windows interpreta el archivo como ANSI y
	// muestra "HernÃ¡ndez" en lugar de "Hernández". Los datos de prueba están
	// llenos de acentos, así que importa.
	buf.Write([]byte{0xEF, 0xBB, 0xBF})

	w := csv.NewWriter(&buf)

	header := []string{"Fecha", "Tipo", "Descripción", "Origen", "Destino", "Monto", "Moneda", "Estado"}
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

		row := []string{
			tx.Timestamp.Format("2006-01-02 15:04:05"),
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

// ExportPDF genera el historial como un PDF.
//
// Se construye a mano en lugar de sumar una librería: el documento es una
// tabla simple, y el formato PDF permite armarlo con primitivas básicas. Evita
// una dependencia de varios megabytes para una función secundaria.
func ExportPDF(transactions []domain.Transaction, accountNumber string, generatedAt time.Time) ([]byte, error) {
	var content bytes.Buffer

	// Los PDF miden en puntos: 1 pt = 1/72 pulgada. Una hoja A4 son 595x842.
	const (
		pageWidth  = 595
		pageHeight = 842
		marginLeft = 40
		startY     = 780
		lineHeight = 16
		minY       = 60
	)

	y := startY

	content.WriteString("BT\n")
	content.WriteString(fmt.Sprintf("/F2 16 Tf\n%d %d Td\n(%s) Tj\n", marginLeft, y, pdfEscape("Historial de transacciones")))
	content.WriteString("ET\n")
	y -= 28

	content.WriteString("BT\n")
	content.WriteString(fmt.Sprintf("/F1 10 Tf\n%d %d Td\n(%s) Tj\n", marginLeft, y,
		pdfEscape("Cuenta: "+accountNumber)))
	content.WriteString("ET\n")
	y -= 14

	content.WriteString("BT\n")
	content.WriteString(fmt.Sprintf("/F1 10 Tf\n%d %d Td\n(%s) Tj\n", marginLeft, y,
		pdfEscape("Generado: "+generatedAt.Format("2006-01-02 15:04:05"))))
	content.WriteString("ET\n")
	y -= 24

	// Línea separadora bajo el encabezado.
	content.WriteString(fmt.Sprintf("0.5 w\n%d %d m\n%d %d l\nS\n", marginLeft, y, pageWidth-marginLeft, y))
	y -= 18

	content.WriteString("BT\n")
	content.WriteString(fmt.Sprintf("/F2 9 Tf\n%d %d Td\n(%s) Tj\n", marginLeft, y,
		pdfEscape("Fecha            Tipo           Monto          Descripción")))
	content.WriteString("ET\n")
	y -= 16

	for _, tx := range transactions {
		if y < minY {
			// Una sola página: el resto se consulta en la aplicación o se
			// exporta a CSV, que no tiene este límite.
			content.WriteString("BT\n")
			content.WriteString(fmt.Sprintf("/F1 9 Tf\n%d %d Td\n(%s) Tj\n", marginLeft, y,
				pdfEscape("... continúa. Exportá a CSV para ver el historial completo.")))
			content.WriteString("ET\n")
			break
		}

		amount := tx.Amount.String()
		if tx.Direction == domain.DirectionOut {
			amount = "-" + amount
		}

		line := fmt.Sprintf("%-16s %-14s %-14s %s",
			tx.Timestamp.Format("2006-01-02"),
			truncate(translateType(tx.Type), 14),
			amount,
			truncate(tx.Description, 40),
		)

		content.WriteString("BT\n")
		content.WriteString(fmt.Sprintf("/F1 9 Tf\n%d %d Td\n(%s) Tj\n", marginLeft, y, pdfEscape(line)))
		content.WriteString("ET\n")
		y -= lineHeight
	}

	return buildPDF(content.String(), pageWidth, pageHeight)
}

// buildPDF arma la estructura del documento con sus objetos y la tabla de
// referencias cruzadas que el formato exige.
func buildPDF(content string, width, height int) ([]byte, error) {
	var pdf bytes.Buffer

	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] "+
			"/Resources << /Font << /F1 5 0 R /F2 6 0 R >> >> /Contents 4 0 R >>", width, height),
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		// Las fuentes base están en todos los lectores de PDF: no hace falta
		// incrustarlas y el archivo queda mucho más liviano.
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>",
	}

	pdf.WriteString("%PDF-1.4\n")

	// La tabla xref necesita el desplazamiento en bytes de cada objeto, así que
	// se van registrando a medida que se escriben.
	offsets := make([]int, len(objects))
	for i, obj := range objects {
		offsets[i] = pdf.Len()
		pdf.WriteString(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, obj))
	}

	xrefOffset := pdf.Len()
	pdf.WriteString(fmt.Sprintf("xref\n0 %d\n", len(objects)+1))
	pdf.WriteString("0000000000 65535 f \n")
	for _, offset := range offsets {
		pdf.WriteString(fmt.Sprintf("%010d 00000 n \n", offset))
	}

	pdf.WriteString(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objects)+1, xrefOffset))

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
			out.WriteString(fmt.Sprintf(`\%03o`, code))
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
