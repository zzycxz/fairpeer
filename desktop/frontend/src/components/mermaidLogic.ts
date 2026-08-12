export function extractMermaidTitle(code: string): string {
  const match = code.match(/---\n\s*title:\s*(.+?)\s*\n---/);
  return match ? match[1].trim() : "";
}

export function sanitizeMermaidCode(code: string): string {
  // Mermaid's parser can be sensitive to trailing/leading whitespace and empty lines
  return code.replace(/\n\s*\n$/g, '\n').trim();
}
