import ts from "typescript"

export interface Hit {
  file: string
  line: number
  text: string
}

const COLOUR = /#[0-9a-fA-F]{3,8}\b|\b(?:rgb|rgba|hsl|hsla|oklch)\(/g

function stripComments(source: string): string {
  return source.replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, " ")).replace(/(^|[^:"'`])\/\/.*$/gm, (m, lead: string) => lead + " ".repeat(m.length - lead.length))
}

export function colourHits(file: string, source: string): Hit[] {
  const hits: Hit[] = []
  stripComments(source)
    .split("\n")
    .forEach((line, i) => {
      for (const m of line.matchAll(COLOUR)) hits.push({ file, line: i + 1, text: m[0] })
    })
  return hits
}

const TEXT_PROPS = new Set(["title", "description", "label", "caption"])

// Text inside these renders in a tip or a dialog body, where the copy budget does not apply.
function exempt(tag: string): boolean {
  return tag === "InfoTip" || tag.startsWith("Dialog") || tag.startsWith("AlertDialog") || tag.startsWith("Sheet")
}

function tagName(node: ts.JsxElement | ts.JsxSelfClosingElement): string {
  const name = ts.isJsxElement(node) ? node.openingElement.tagName : node.tagName
  return name.getText()
}

// staticText is a literal's text, or a template literal's static parts joined; null for anything computed.
function staticText(expr: ts.Expression | undefined): string | null {
  if (expr === undefined) return null
  if (ts.isStringLiteral(expr) || ts.isNoSubstitutionTemplateLiteral(expr)) return expr.text
  if (ts.isTemplateExpression(expr)) return expr.head.text + expr.templateSpans.map((s) => s.literal.text).join("")
  return null
}

export function longTexts(file: string, source: string, max: number): Hit[] {
  const sf = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  const hits: Hit[] = []
  const add = (node: ts.Node, text: string) => {
    const t = text.replace(/&[a-z]+;/g, "'").replace(/\s+/g, " ").trim()
    if (t.length > max) hits.push({ file, line: sf.getLineAndCharacterOfPosition(node.getStart()).line + 1, text: t })
  }
  const visit = (node: ts.Node): void => {
    if ((ts.isJsxElement(node) || ts.isJsxSelfClosingElement(node)) && exempt(tagName(node))) return
    if (ts.isJsxText(node)) add(node, node.text)
    else if (ts.isJsxExpression(node) && ts.isJsxElement(node.parent)) {
      const text = staticText(node.expression)
      if (text !== null) add(node, text)
    } else if (ts.isJsxAttribute(node) && TEXT_PROPS.has(node.name.getText()) && node.initializer !== undefined) {
      const init = node.initializer
      const text = ts.isStringLiteral(init) ? init.text : ts.isJsxExpression(init) ? staticText(init.expression) : null
      if (text !== null) add(node, text)
    }
    ts.forEachChild(node, visit)
  }
  visit(sf)
  return hits
}
