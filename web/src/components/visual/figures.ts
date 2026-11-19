// The share of value in max as a whole percentage, clamped to 0-100; a zero
// limit is 0%.
export function percent(value: number, max: number): number {
  if (max <= 0) return 0
  return Math.round(Math.min(100, Math.max(0, (value / max) * 100)))
}

export function figureLabel(label: string, value: number, max: number, unit?: string): string {
  return `${label}: ${value} of ${max}${unit ? ` ${unit}` : ""} (${percent(value, max)}%)`
}
