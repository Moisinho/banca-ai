import { describe, expect, it } from "vitest";

import {
  formatSigned,
  fromCents,
  isValidAmount,
  normalizeAmount,
  toCents,
} from "./money";

describe("toCents", () => {
  it("convierte montos decimales a centavos", () => {
    expect(toCents("1234.56")).toBe(123456);
    expect(toCents("100")).toBe(10000);
    expect(toCents("0.01")).toBe(1);
    expect(toCents("10.5")).toBe(1050);
  });

  it("maneja los montos de los datos de prueba", () => {
    expect(toCents("32354.53")).toBe(3235453);
    expect(toCents("4999.65")).toBe(499965);
    expect(toCents("49982.36")).toBe(4998236);
  });

  it("acepta separadores de miles", () => {
    expect(toCents("1,234.56")).toBe(123456);
  });

  it("conserva el signo negativo", () => {
    expect(toCents("-25.50")).toBe(-2550);
  });
});

describe("fromCents", () => {
  it("devuelve el formato decimal con dos decimales", () => {
    expect(fromCents(123456)).toBe("1234.56");
    expect(fromCents(1)).toBe("0.01");
    expect(fromCents(10000)).toBe("100.00");
    expect(fromCents(105)).toBe("1.05");
  });

  it("conserva el signo negativo", () => {
    expect(fromCents(-2550)).toBe("-25.50");
  });
});

/**
 * Este es el test que justifica trabajar con enteros.
 *
 * Sumar 0.10 diez veces en punto flotante NO da 1.00 exacto, porque 0.1 no
 * tiene representación finita en binario. Con centavos el resultado es exacto.
 */
describe("aritmética con centavos", () => {
  it("no acumula error de redondeo", () => {
    let total = 0;
    for (let i = 0; i < 10; i++) {
      total += toCents("0.10");
    }

    expect(total).toBe(100);
    expect(fromCents(total)).toBe("1.00");

    // La demostración del problema que estamos evitando.
    let conFloat = 0;
    for (let i = 0; i < 10; i++) {
      conFloat += 0.1;
    }
    expect(conFloat).not.toBe(1);
  });

  it("parsear y volver a formatear no altera el valor", () => {
    const montos = ["32354.53", "4999.65", "995.76", "1633.55", "0.01"];

    for (const monto of montos) {
      expect(fromCents(toCents(monto))).toBe(monto);
    }
  });
});

describe("isValidAmount", () => {
  it("acepta montos válidos", () => {
    expect(isValidAmount("100")).toBe(true);
    expect(isValidAmount("1234.56")).toBe(true);
    expect(isValidAmount("0.01")).toBe(true);
    expect(isValidAmount("10,50")).toBe(true);
  });

  it("rechaza montos inválidos", () => {
    expect(isValidAmount("")).toBe(false);
    expect(isValidAmount("abc")).toBe(false);
    expect(isValidAmount("0")).toBe(false);
    expect(isValidAmount("-50")).toBe(false);
    expect(isValidAmount("10.999")).toBe(false);
  });
});

describe("normalizeAmount", () => {
  it("convierte la coma decimal en punto", () => {
    expect(normalizeAmount("10,50")).toBe("10.50");
    expect(normalizeAmount("  25.75  ")).toBe("25.75");
  });
});

describe("formatSigned", () => {
  it("marca la dirección del movimiento con el signo", () => {
    // El signo hace legible la dirección sin depender sólo del color.
    expect(formatSigned("100.00", "out")).toContain("−");
    expect(formatSigned("100.00", "in")).toContain("+");
  });
});
