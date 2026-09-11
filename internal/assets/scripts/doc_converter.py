import sys
import json

def main():
    if len(sys.argv) < 2:
        print(json.dumps({"error": "No file path provided", "text": "", "title": ""}))
        sys.exit(1)
        
    path = sys.argv[1]
    result = {"text": "", "title": "", "error": ""}
    
    try:
        try:
            from markitdown import MarkItDown  # noqa: F401
        except ImportError:
            # Nothing in the product installs markitdown today; self-install
            # so the converter works wherever python/pip exists.
            import subprocess, sys as _sys
            pip_cmd = [_sys.executable, "-m", "pip", "install", "--user", "markitdown",
                       "--quiet", "--disable-pip-version-check"]
            try:
                subprocess.check_call(pip_cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            except subprocess.CalledProcessError:
                # PEP 668 externally-managed interpreters reject --user; retry
                # with the override flag.
                subprocess.check_call(
                    pip_cmd + ["--break-system-packages"],
                    stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                )
        from markitdown import MarkItDown
        md = MarkItDown()
        res = md.convert(path)
        result["text"] = res.text_content
    except Exception as e:
        result["error"] = str(e)
        
    print(json.dumps(result))

if __name__ == "__main__":
    main()
