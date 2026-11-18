import type { CarrierInfo } from '../types/tracking';

export interface QuickSampleChip {
  code: string;
  carrier: string;
  hint: string;
}

export const QUICK_SAMPLE_CHIPS: QuickSampleChip[] = [
  {
    code: 'AA123456789BR',
    carrier: 'Correios',
    hint: 'Alocado no CDD p/ Retirada',
  },
  {
    code: 'LGG84920194BR',
    carrier: 'Loggi',
    hint: 'Saiu para Entrega',
  },
  {
    code: 'AZL577901234BR',
    carrier: 'Azul Cargo Express',
    hint: 'Em Trânsito',
  },
  {
    code: 'MND994827162BR',
    carrier: 'Mandaê',
    hint: 'Alocado no CDD',
  },
  {
    code: 'MGL339104857BR',
    carrier: 'Magalog',
    hint: 'Entregue',
  },
];

interface LocalCarrierRule {
  code: string;
  name: string;
  regex: RegExp;
  confidence: number;
  patternDescription: string;
  regexPattern: string;
}

// Mirrors backend/internal/carrier/detector.go rules for 0ms instant typing feedback
const LOCAL_CARRIER_RULES: LocalCarrierRule[] = [
  {
    code: 'correios',
    name: 'Correios',
    regex: /^[A-Z]{2}\d{9}[A-Z]{2}$/i,
    confidence: 0.99,
    patternDescription:
      'Padrão S10 UPU: 2 letras iniciais + 9 dígitos + 2 letras do país (ex: AA123456789BR)',
    regexPattern: '^[A-Z]{2}\\d{9}[A-Z]{2}$',
  },
  {
    code: 'azul_express',
    name: 'Azul Cargo Express',
    regex: /^(AZ|AZL|800)\d{8,11}[A-Z0-9]{0,2}$|^AZUL[A-Z0-9]{6,12}$|^577\d{8}$/i,
    confidence: 0.97,
    patternDescription: 'AWB aéreo 577 + 8 dígitos ou prefixos AZ/AZL/AZUL/800',
    regexPattern: '^(AZ|AZL|800)\\d{8,11}[A-Z0-9]{0,2}$|^AZUL[A-Z0-9]{6,12}$|^577\\d{8}$',
  },
  {
    code: 'loggi',
    name: 'Loggi',
    regex: /^(LGG|LOG|LG)[A-Z0-9]{8,14}$/i,
    confidence: 0.97,
    patternDescription:
      'Prefixos LGG, LOG ou LG seguidos de 8 a 14 caracteres alfanuméricos',
    regexPattern: '^(LGG|LOG|LG)[A-Z0-9]{8,14}$',
  },
  {
    code: 'mandae',
    name: 'Mandaê',
    regex: /^(MND|MAN|MD)[A-Z0-9]{7,13}$|^99\d{9,11}$/i,
    confidence: 0.96,
    patternDescription:
      'Prefixos MND, MAN, MD com 7-13 caracteres ou numérico iniciado por 99',
    regexPattern: '^(MND|MAN|MD)[A-Z0-9]{7,13}$|^99\\d{9,11}$',
  },
  {
    code: 'magalog',
    name: 'Magalog (Magalu Entregas)',
    regex: /^(MGL|MAG|MLG)[A-Z0-9]{8,14}$/i,
    confidence: 0.97,
    patternDescription:
      'Prefixos MGL, MAG ou MLG seguidos de 8 a 14 caracteres alfanuméricos',
    regexPattern: '^(MGL|MAG|MLG)[A-Z0-9]{8,14}$',
  },
  {
    code: 'jadlog',
    name: 'Jadlog',
    regex: /^(JAD|JD)\d{9,14}$/i,
    confidence: 0.95,
    patternDescription:
      'Prefixo JAD/JD seguido de 9 a 14 dígitos ou código numérico de remessa com 14 dígitos',
    regexPattern: '^(JAD|JD)\\d{9,14}$|^\\d{14}$',
  },
  {
    code: 'total_express',
    name: 'Total Express',
    regex: /^(TEX|TOT)[A-Z0-9]{8,13}$|^7\d{10,12}$/i,
    confidence: 0.95,
    patternDescription:
      'Prefixos TEX ou TOT seguidos de 8-13 caracteres ou numérico iniciado por 7 (11-13 dígitos)',
    regexPattern: '^(TEX|TOT)[A-Z0-9]{8,13}$|^7\\d{10,12}$',
  },
  {
    code: 'sequoia',
    name: 'Sequoia / Shopee Xpress',
    regex: /^(BR\d{12,13}[A-Z0-9]?|SEQ[A-Z0-9]{8,12})$/i,
    confidence: 0.96,
    patternDescription:
      'Padrão Shopee Xpress BR + 12-13 dígitos (com sufixo opcional) ou Sequoia SEQ + 8-12 caracteres',
    regexPattern: '^(BR\\d{12,13}[A-Z0-9]?|SEQ[A-Z0-9]{8,12})$',
  },
  {
    code: 'cainiao',
    name: 'Cainiao / AliExpress',
    regex: /^LP\d{12,15}$/i,
    confidence: 0.97,
    patternDescription:
      'Prefixo internacional LP seguido de 12 a 15 dígitos (Cainiao / AliExpress)',
    regexPattern: '^LP\\d{12,15}$',
  },
  {
    code: 'jt_express',
    name: 'J&T Express',
    regex: /^(JT|888)\d{10,14}$/i,
    confidence: 0.96,
    patternDescription: 'Prefixos JT ou 888 seguidos de 10 a 14 dígitos',
    regexPattern: '^(JT|888)\\d{10,14}$',
  },
  {
    code: 'jadlog',
    name: 'Jadlog',
    regex: /^\d{14}$/,
    confidence: 0.92,
    patternDescription: 'Código numérico de remessa com 14 dígitos (Jadlog)',
    regexPattern: '^(JAD|JD)\\d{9,14}$|^\\d{14}$',
  },
];

export function sanitizeTrackingInput(raw: string): string {
  return raw.toUpperCase().replace(/[^A-Z0-9]/g, '');
}

export function detectCarrierLocally(
  rawCode: string,
  backendCarriers?: CarrierInfo[],
): CarrierInfo | null {
  const code = sanitizeTrackingInput(rawCode);
  if (code.length < 4) {
    return null;
  }

  for (const rule of LOCAL_CARRIER_RULES) {
    if (rule.regex.test(code)) {
      return {
        code: rule.code,
        name: rule.name,
        detectedBy: 'regex',
        confidence: rule.confidence,
        patternDescription: rule.patternDescription,
        regexPattern: rule.regexPattern,
      };
    }
  }

  if (backendCarriers && backendCarriers.length > 0) {
    for (const carrier of backendCarriers) {
      if (!carrier.regexPattern) continue;
      try {
        const re = new RegExp(carrier.regexPattern, 'i');
        if (re.test(code)) {
          return {
            ...carrier,
            detectedBy: 'regex',
            confidence: carrier.confidence || 0.9,
          };
        }
      } catch {
        // Ignore malformed external pattern
      }
    }
  }

  // Partial prefix hints while user is still typing
  if (code.length >= 3) {
    if (code.startsWith('LGG') || code.startsWith('LOG')) {
      return {
        code: 'loggi',
        name: 'Loggi (provável)',
        detectedBy: 'heuristic',
        confidence: 0.6,
        patternDescription: 'Prefixo parcial Loggi — continue digitando',
      };
    }
    if (code.startsWith('AZL') || code.startsWith('AZU') || code.startsWith('577')) {
      return {
        code: 'azul_express',
        name: 'Azul Cargo Express (provável)',
        detectedBy: 'heuristic',
        confidence: 0.6,
        patternDescription: 'Prefixo parcial Azul Cargo — continue digitando',
      };
    }
    if (code.startsWith('MND') || code.startsWith('MAN')) {
      return {
        code: 'mandae',
        name: 'Mandaê (provável)',
        detectedBy: 'heuristic',
        confidence: 0.6,
        patternDescription: 'Prefixo parcial Mandaê — continue digitando',
      };
    }
    if (code.startsWith('MGL') || code.startsWith('MAG') || code.startsWith('MLG')) {
      return {
        code: 'magalog',
        name: 'Magalog (Magalu Entregas) (provável)',
        detectedBy: 'heuristic',
        confidence: 0.6,
        patternDescription: 'Prefixo parcial Magalog — continue digitando',
      };
    }
  }

  return null;
}
