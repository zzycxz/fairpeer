"""PDF text extraction combining pdfplumber (tables + text) and PaddleOCR (scanned pages).

Usage:
    python ocr_pdf.py <pdf_path> [--lang ch]

Pipeline per page:
    1. pdfplumber extracts tables (validated) + plain text.
    2. If text < threshold, page is likely scanned → PaddleOCR.
    3. Results merged: [table] blocks + plain text paragraphs.

Table validation (防止误识别):
    - >= 2 rows, >= 2 columns.
    - All rows same column count (±30% tolerance for merged cells).
    - >= 50% cells non-empty.
    - Average cell length < 200 chars.
"""

import json
import os
import sys
import tempfile

# Non-fatal notices collected during extraction (dependency installs, per-page
# OCR failures). Returned as a structured "warnings" array instead of being
# spliced into the document text — an error string interleaved with contract
# clauses reads like a footnote and invites the model to hallucinate around it.
WARNINGS = []


def _install(pkg: str):
    import subprocess
    # First-run installs pull hundreds of MB (paddlepaddle) and can take
    # minutes. Make that visible on stderr (swallowed output made the tool
    # card look hung) and record it so the model can tell the user why the
    # first read was slow.
    print(f"[ocr] installing {pkg} (first run; may take a few minutes)...",
          file=sys.stderr, flush=True)
    cmd = [sys.executable, "-m", "pip", "install", pkg,
           "--quiet", "--disable-pip-version-check"]
    try:
        subprocess.check_call(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        WARNINGS.append(f"installed dependency {pkg} during this run (first-run setup)")
    except subprocess.CalledProcessError:
        # PEP 668 externally-managed interpreters (Debian 12+/Homebrew) reject
        # plain --user installs; retry with the override flag.
        subprocess.check_call(
            cmd + ["--user", "--break-system-packages"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        WARNINGS.append(f"installed dependency {pkg} during this run (first-run setup)")


def _ensure_pdfplumber():
    try:
        import pdfplumber  # noqa: F401
    except ImportError:
        _install("pdfplumber")


def _ensure_ocr():
    try:
        import paddleocr  # noqa: F401
        ver = getattr(paddleocr, "__version__", "2") or "2"  # 老版本无该属性
        if int(ver.split(".")[0]) >= 3:
            # 3.x removed the use_angle_cls/show_log constructor kwargs used
            # below — pin to the 2.x API this script targets.
            raise ImportError("paddleocr>=3 unsupported; reinstalling 2.x")
    except ImportError:
        _install("paddleocr<3.0")  # argv list: no shell, < is safe
        _install("paddlepaddle<3.0")  # 未锁版本会装 3.x，与 2.x paddleocr API 不配套


def _ensure_fitz():
    try:
        import fitz  # noqa: F401
    except ImportError:
        _install("PyMuPDF")


# ---------------------------------------------------------------------------
# Table validation
# ---------------------------------------------------------------------------

def _is_valid_table(table: list, min_rows: int = 2, min_cols: int = 2) -> bool:
    """Validate that a detected table is likely a real table."""
    if not table or len(table) < min_rows:
        return False

    col_counts = {}
    for row in table:
        n = len(row)
        col_counts[n] = col_counts.get(n, 0) + 1

    most_common_cols = max(col_counts, key=col_counts.get)
    if most_common_cols < min_cols:
        return False

    # Allow up to 30% rows with different column count (merged cells).
    mismatched = sum(v for k, v in col_counts.items() if k != most_common_cols)
    if mismatched > len(table) * 0.3:
        return False

    # At least 50% cells non-empty.
    total, filled = 0, 0
    for row in table:
        for cell in row:
            total += 1
            if cell and str(cell).strip():
                filled += 1
    if total == 0 or filled / total < 0.5:
        return False

    # Average cell length sanity check.
    avg_len = sum(len(str(c or "")) for row in table for c in row) / total
    if avg_len > 200:
        return False

    return True


def _table_to_tsv(table: list) -> str:
    """Convert a table to TSV string."""
    lines = []
    for row in table:
        cells = [str(c or "").replace("\n", " ").replace("\t", " ") for c in row]
        lines.append("\t".join(cells))
    return "\n".join(lines)


# ---------------------------------------------------------------------------
# OCR for scanned pages
# ---------------------------------------------------------------------------

def _ocr_page(pdf_path: str, page_idx: int, ocr_engine) -> str:
    """Render a page to image and run PaddleOCR."""
    import fitz

    tmp = tempfile.NamedTemporaryFile(suffix=f"_p{page_idx}.png", delete=False)
    # Windows 上 unlink 要求句柄已关闭：创建后立即 close，只留路径。
    tmp.close()
    doc = None
    try:
        doc = fitz.open(pdf_path)
        page = doc[page_idx]
        mat = fitz.Matrix(2, 2)  # 2x for better accuracy
        pix = page.get_pixmap(matrix=mat)
        pix.save(tmp.name)
    finally:
        if doc is not None:
            doc.close()

    try:
        result = ocr_engine.ocr(tmp.name, cls=True)
        lines = []
        if result and result[0]:
            for line in result[0]:
                if isinstance(line, (list, tuple)) and len(line) >= 2:
                    text_info = line[1]
                    if isinstance(text_info, (list, tuple)):
                        lines.append(str(text_info[0]))
                    else:
                        lines.append(str(text_info))
        return "\n".join(lines)
    finally:
        try:
            os.unlink(tmp.name)
        except OSError:
            pass  # 渲染已成功且 OCR 结束；Windows 上偶发句柄延迟时容忍


# ---------------------------------------------------------------------------
# Main pipeline
# ---------------------------------------------------------------------------

_MIN_TEXT = 50  # chars per page; below → likely scanned


def extract_pdf(pdf_path: str, lang: str = "ch", first: int = 1, last: int = 0) -> tuple:
    """Extract text from PDF pages [first..last] (1-based inclusive; last=0 → end).

    Tables get [table]...[/table] wrapping. Returns (text, pages_total) so the
    Go caller can drive page-range batches without a separate probe."""
    _ensure_pdfplumber()
    import pdfplumber

    all_parts = []
    scanned_indices = []
    pages_total = 0

    with pdfplumber.open(pdf_path) as pdf:
        pages_total = len(pdf.pages)
        page_slice = pdf.pages[first - 1: last if last > 0 else None]
        for offset, page in enumerate(page_slice):
            i = (first - 1) + offset  # absolute 0-based page index
            # Tables.
            tables = page.extract_tables() or []
            valid_tsvs = []
            for t in tables:
                if _is_valid_table(t):
                    valid_tsvs.append(_table_to_tsv(t))

            # Plain text.
            plain = (page.extract_text() or "").strip()

            # Build page output.
            parts = []
            for tsv in valid_tsvs:
                parts.append(f"[table]\n{tsv}[/table]")
            if plain:
                parts.append(plain)

            page_text = "\n\n".join(parts)

            if len(page_text.strip()) < _MIN_TEXT:
                scanned_indices.append(i)
            elif page_text.strip():
                all_parts.append(page_text.strip())

    # OCR scanned pages.
    if scanned_indices:
        _ensure_ocr()
        _ensure_fitz()
        from paddleocr import PaddleOCR
        ocr = PaddleOCR(use_angle_cls=True, lang=lang, show_log=False)
        for idx in scanned_indices:
            try:
                text = _ocr_page(pdf_path, idx, ocr)
                if text.strip():
                    all_parts.append(f"[page {idx + 1}]\n{text.strip()}")
                else:
                    all_parts.append(f"[page {idx + 1} unavailable: OCR returned no text]")
                    WARNINGS.append(f"page {idx + 1}: OCR returned no text; content may be missing")
            except Exception as e:
                # Never splice the exception into the clauses — mark the page
                # as a whole as unavailable and keep the detail in warnings.
                all_parts.append(f"[page {idx + 1} unavailable: scanned page could not be OCR'd]")
                WARNINGS.append(f"page {idx + 1}: OCR failed ({e}); content may be missing")

    return "\n\n".join(all_parts), pages_total


def main():
    # Piped stdout on Windows defaults to the ANSI codepage, so non-ASCII text
    # would either raise UnicodeEncodeError on print or reach the Go side as
    # mojibake that json.Unmarshal rejects. Force UTF-8 on both streams.
    import sys
    # 两条流独立守卫（管道/重定向下任一条可能缺少 reconfigure）。
    if hasattr(sys.stdout, "reconfigure"):
        sys.stdout.reconfigure(encoding="utf-8")
    if hasattr(sys.stderr, "reconfigure"):
        sys.stderr.reconfigure(encoding="utf-8")

    if len(sys.argv) < 2:
        print(json.dumps({"error": "usage: ocr_pdf.py <pdf_path> [--lang ch]"}))
        print("usage: ocr_pdf.py <pdf_path> [--lang ch]", file=sys.stderr, flush=True)
        sys.exit(1)

    pdf_path = sys.argv[1]
    lang = "ch"
    if "--lang" in sys.argv:
        idx = sys.argv.index("--lang")
        if idx + 1 < len(sys.argv):
            lang = sys.argv[idx + 1]

    if not os.path.isfile(pdf_path):
        print(json.dumps({"error": f"file not found: {pdf_path}"}))
        print(f"file not found: {pdf_path}", file=sys.stderr, flush=True)
        sys.exit(1)

    # Page-range batching (spec R2/F-E1): the Go caller drives [first..last]
    # batches of ~20 pages so no single run hits the 10-minute timeout on
    # long scanned PDFs; partial success accumulates across batches.
    first, last = 1, 0
    for flag, minimum in (("--first", 1), ("--last", 0)):
        if flag in sys.argv:
            idx = sys.argv.index(flag)
            if idx + 1 >= len(sys.argv):
                print(json.dumps({"error": f"missing value after {flag}"}))
                print(f"missing value after {flag}", file=sys.stderr, flush=True)
                sys.exit(1)
            try:
                value = int(sys.argv[idx + 1])
            except ValueError:
                print(json.dumps({"error": f"{flag} expects an integer"}))
                print(f"{flag} expects an integer", file=sys.stderr, flush=True)
                sys.exit(1)
            if flag == "--first":
                first = max(1, value)
            else:
                last = max(0, value)
    if last and last < first:
        print(json.dumps({"error": f"--last ({last}) must be >= --first ({first})"}))
        print(f"--last ({last}) must be >= --first ({first})", file=sys.stderr, flush=True)
        sys.exit(1)

    try:
        text, pages_total = extract_pdf(pdf_path, lang, first, last)
        print(json.dumps({
            "text": text, "warnings": WARNINGS,
            "pages_total": pages_total, "first": first, "last": last or pages_total,
        }, ensure_ascii=False))
    except Exception as e:
        print(json.dumps({"error": str(e), "warnings": WARNINGS}, ensure_ascii=False))
        print(str(e), file=sys.stderr, flush=True)
        sys.exit(1)


if __name__ == "__main__":
    main()
