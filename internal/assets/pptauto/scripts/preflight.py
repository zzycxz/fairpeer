#!/usr/bin/env python3
"""One-shot preflight for the SVG route: baseline restore + template colors +
visual merge + brand preset + project init + consolidated config summary.

Replaces what used to be several separate agent round trips (ls template →
extract_template_colors → merge_vlm_style → read template_config.json →
project_manager init) with ONE bash call. The printed JSON is everything the
model needs from Step 0 / Step 3 / Step 4: final colors, fonts, mode, whether
references exist, and the created project_dir.

Color source precedence (highest last):
  baseline colors (embedded defaults)
    → extract_template_colors.py (XML hex + clustered image families)
    → merge_vlm_style.py (VLM: fills gaps; reference image may override)
    → brand preset (--preset china-mobile; skips recognition entirely)

template_config.json is MUTATED IN PLACE across runs, so preflight first
restores colors from the stashed _baseline_colors (captured on the first ever
run) — without this, one run's template colors would leak into the next run
that picked no template.

Usage:
    python3 preflight.py <project_name> [--preset <brand_id>]  # init included
    python3 preflight.py [--preset <brand_id>]                 # colors only
    python3 preflight.py --list-presets

Exit code is 0 unless project init fails; sub-step statuses live in the JSON
(steps[].rc) so the model can decide instead of parsing prose.
"""
import argparse
import json
import re
import shutil
import subprocess
import sys
from pathlib import Path

SCRIPTS = Path(__file__).resolve().parent
SKILL_DIR = SCRIPTS.parent
FAIRPEER_DIR = Path.home() / ".fairpeer"
PRESET_DIR = SKILL_DIR / "references" / "brand-presets"


def run(cmd):
    p = subprocess.run(cmd, capture_output=True, text=True, encoding="utf-8", errors="replace")
    return p.returncode, ((p.stdout or "") + (p.stderr or "")).strip()


def deep_merge(dst, src):
    """Recursive dict merge: src wins on leaf conflicts."""
    for k, v in src.items():
        if isinstance(v, dict) and isinstance(dst.get(k), dict):
            deep_merge(dst[k], v)
        else:
            dst[k] = v


def restore_baseline_colors(cfg_path):
    """Undo the previous run's mutations (colors / rules / default_prompt /
    _template / _preset) by restoring a full snapshot taken on the first ever
    run. `mode` is preserved — the desktop settings panel writes fast/validate
    there via SyncPPTMode and that choice must survive preflight.

    Returns True if a snapshot was restored. The snapshot itself is captured
    before any mutation, so the embedded defaults survive forever.
    """
    try:
        cfg = json.loads(cfg_path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return False
    snapshot = cfg.get("_baseline_snapshot")
    if isinstance(snapshot, dict):
        restored = json.loads(json.dumps(snapshot))  # deep copy
        restored["_baseline_snapshot"] = snapshot
        restored["mode"] = cfg.get("mode", restored.get("mode", "fast"))
        cfg_path.write_text(json.dumps(restored, ensure_ascii=False, indent=2), encoding="utf-8")
        return True
    cfg["_baseline_snapshot"] = json.loads(json.dumps(cfg))
    cfg_path.write_text(json.dumps(cfg, ensure_ascii=False, indent=2), encoding="utf-8")
    return False


def apply_preset(cfg_path, preset_id):
    """Apply a brand preset: colors wholesale + rules merged + template seeded.

    Returns (applied: bool, note: str). Seed only when the user has NOT picked
    their own template — an explicit pick always wins over seeding.
    """
    preset_path = PRESET_DIR / f"{preset_id}.json"
    if not preset_path.is_file():
        return False, f"preset not found: {preset_path}"
    preset = json.loads(preset_path.read_text(encoding="utf-8"))

    cfg = json.loads(cfg_path.read_text(encoding="utf-8"))
    if isinstance(preset.get("colors"), dict):
        cfg.setdefault("colors", {}).update(preset["colors"])
    if isinstance(preset.get("rules"), dict):
        deep_merge(cfg.setdefault("rules", {}), preset["rules"])
    if preset.get("default_prompt_style"):
        cfg.setdefault("default_prompt", {})["style"] = preset["default_prompt_style"]
    cfg["_preset"] = {"id": preset.get("id", preset_id), "name": preset.get("name", "")}
    cfg_path.write_text(json.dumps(cfg, ensure_ascii=False, indent=2), encoding="utf-8")

    seed = preset.get("template_seed")
    seeded = ""
    tpl = FAIRPEER_DIR / "ppt-template.pptx"
    if seed and not tpl.exists():
        src = SKILL_DIR / "templates" / seed
        if src.is_file():
            FAIRPEER_DIR.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(str(src), str(tpl))
            seeded = f"; template seeded from templates/{seed}"
    return True, f"colors+rules applied{seeded}"


def list_presets():
    out = []
    if PRESET_DIR.is_dir():
        for p in sorted(PRESET_DIR.glob("*.json")):
            try:
                d = json.loads(p.read_text(encoding="utf-8"))
                out.append({"id": d.get("id", p.stem), "name": d.get("name", ""),
                            "keywords": d.get("keywords", [])})
            except (OSError, ValueError):
                continue
    return out


def main():
    ap = argparse.ArgumentParser(description="ppt-auto preflight: colors + config + project init")
    ap.add_argument("project_name", nargs="?", default="", help="project name (omit: no init)")
    ap.add_argument("--preset", default=None,
                    help="brand preset id (see references/brand-presets/); skips color recognition")
    ap.add_argument("--list-presets", action="store_true", help="print available presets and exit")
    args = ap.parse_args()

    if args.list_presets:
        print(json.dumps({"presets": list_presets()}, ensure_ascii=False, indent=2))
        return

    project_name = args.project_name.strip() if args.project_name else ""
    py = sys.executable or "python3"
    cfg_path = SKILL_DIR / "template_config.json"

    summary = {
        "has_template": False,
        "reference_style": False,
        "pdf_pages": 0,
        "project_dir": "",
        "preset": args.preset or "",
        "colors_source": "baseline",
        "steps": [],
    }

    restore_baseline_colors(cfg_path)

    tpl = FAIRPEER_DIR / "ppt-template.pptx"
    summary["has_template"] = tpl.exists()

    if args.preset:
        ok, note = apply_preset(cfg_path, args.preset)
        summary["steps"].append({"step": "preset", "rc": 0 if ok else 1, "out": note})
        if ok:
            summary["colors_source"] = f"preset:{args.preset}"
            summary["has_template"] = tpl.exists()
        else:
            print(f"[preflight] WARN preset failed: {note}", file=sys.stderr)
    else:
        if tpl.exists():
            rc, out = run([py, str(SCRIPTS / "extract_template_colors.py"), str(tpl), str(cfg_path)])
            summary["steps"].append({"step": "extract_template_colors", "rc": rc, "out": out[-400:]})
            if rc == 0:
                summary["colors_source"] = "template-extract"
        # Merge runs whenever ANY VLM style file exists — a reference image's
        # colors must reach the config even when the user picked no template
        # (S-21: the merge used to be gated on the template and silently skipped).
        has_style = any((FAIRPEER_DIR / name).exists()
                        for name in ("ppt-template-style.json", "reference-style.json"))
        if has_style:
            rc, out = run([py, str(SCRIPTS / "merge_vlm_style.py"), str(cfg_path)])
            summary["steps"].append({"step": "merge_vlm_style", "rc": rc, "out": out[-400:]})
            if rc == 0:
                if (FAIRPEER_DIR / "reference-style.json").exists():
                    summary["colors_source"] = "reference-vlm"
                else:
                    summary["colors_source"] = "template-vlm"

    summary["reference_style"] = (FAIRPEER_DIR / "reference-style.json").exists()
    pages_dir = FAIRPEER_DIR / "pdf-pages"
    if pages_dir.is_dir():
        summary["pdf_pages"] = len(list(pages_dir.glob("page-*.json")))

    if project_name:
        rc, out = run([py, str(SCRIPTS / "project_manager.py"), "init", project_name, "--format", "ppt169"])
        summary["steps"].append({"step": "project_init", "rc": rc, "out": out[-400:]})
        m = re.search(r"Project created:\s*(.+)", out)
        if m:
            summary["project_dir"] = m.group(1).strip()
        if rc != 0:
            print(json.dumps(summary, ensure_ascii=False, indent=2))
            sys.exit(1)

    try:
        cfg = json.loads(cfg_path.read_text(encoding="utf-8"))
        summary["colors"] = cfg.get("colors", {})
        summary["fonts"] = cfg.get("fonts", {})
        summary["mode"] = cfg.get("mode", "fast")
    except Exception as exc:  # unreadable/missing config — report, don't crash
        summary["config_error"] = str(exc)

    print(json.dumps(summary, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
