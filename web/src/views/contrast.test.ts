import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

// jsdom does not apply index.css, so read the stylesheet and check the colours.
const css = readFileSync(join(process.cwd(), 'src/index.css'), 'utf8')

const lin = (c: number) => (c /= 255) <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
function luminance(hex: string) {
  const n = parseInt(hex.slice(1), 16)
  return 0.2126 * lin((n >> 16) & 255) + 0.7152 * lin((n >> 8) & 255) + 0.0722 * lin(n & 255)
}
function ratio(a: string, b: string) {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}
const light = css.split('@media (prefers-color-scheme: dark)')[0]
const dark = css.split('@media (prefers-color-scheme: dark)')[1].split('\n}\n')[0]
const v = (block: string, name: string) => block.match(new RegExp(`${name}:\\s*(#[0-9a-fA-F]{6})`))![1]

describe('colour contrast (WCAG 1.4.3, 4.5:1)', () => {
  it('.form-error uses the error text colour, not the status colour', () => {
    expect(css).toMatch(/\.form-error\s*\{\s*color:\s*var\(--color-text-error\)/)
  })
  it('error text reaches 4.5:1 on the card in light and dark', () => {
    expect(ratio(v(light, '--color-text-error'), v(light, '--color-surface-card'))).toBeGreaterThanOrEqual(4.5)
    expect(ratio(v(dark, '--color-text-error'), v(dark, '--color-surface-card'))).toBeGreaterThanOrEqual(4.5)
  })
  it('the mobile More button uses the bar surface and the app text colour, not the browser face', () => {
    const rule = css.match(/\nbutton\.bottom-nav-link\s*\{([^}]*)\}/)![1]
    expect(rule).toMatch(/background:\s*var\(--color-surface-elevated\)/)
    expect(rule).toMatch(/color:\s*var\(--color-text-primary\)/)
    // The bar itself is painted with --color-surface-elevated.
    expect(css).toMatch(/\.bottom-nav\s*\{[^}]*background:\s*var\(--color-surface-elevated\)/)
    for (const block of [light, dark]) {
      expect(ratio(v(block, '--color-text-primary'), v(block, '--color-surface-elevated'))).toBeGreaterThanOrEqual(4.5)
    }
  })
  it('the More button shows a visible focus ring', () => {
    expect(css).toMatch(/button\.bottom-nav-link:focus-visible\s*\{[^}]*outline:\s*2px solid var\(--color-focus-ring\)/)
    expect(ratio(v(light, '--color-focus-ring'), v(light, '--color-surface-elevated'))).toBeGreaterThanOrEqual(3)
  })
  it('dialogs take the app text colour instead of the browser black', () => {
    expect(css).toMatch(/dialog\.card\s*\{\s*color:\s*var\(--color-text-primary\)/)
    expect(ratio(v(dark, '--color-text-primary'), v(dark, '--color-surface-card'))).toBeGreaterThanOrEqual(4.5)
  })
})
