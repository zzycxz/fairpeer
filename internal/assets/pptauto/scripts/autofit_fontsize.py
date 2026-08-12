#!/usr/bin/env python3
"""Font-size autofit for ppt-auto (Phase 3 of ppt-vision-enhancement-spec).

Computes a font-size ramp (title / card-title / body) from:
  - baseline : from template_config.json (default 26 / 18 / 14)
  - density  : from the VLM's LAYOUT judgment on the reference image
               (high / medium / low — how packed the reference page is)
  - ratio    : from the VLM's FORMAT judgment (title is ~Nx body)

WHY this exists (the "judgment, not guessing" rule):
  Font sizes were hardcoded (26/18/14). The VLM cannot see exact pixels, so we do
  NOT ask it for px values (that would be guessing). Instead we use its RELIABLE
  qualitative judgments — density and title/body ratio — as multipliers on the
  template's baseline. The result tracks the reference's feel (denser → smaller,
  sparser → larger; title/body hierarchy honored) while staying anchored to the
  template's actual sizes. Per ppt-vision-enhancement-spec §五.

Usage:
    python autofit_fontsize.py --density high --ratio 2.0
    # → {"title": 24, "card_title": 15, "body": 12, ...}

    python autofit_fontsize.py --density medium              # ratio defaults to 1.8
    python autofit_fontsize.py --baseline 28,20,14 --ratio 1.6
"""
import argparse
import json
import sys

# Density multipliers: a denser reference → slightly smaller fonts so more content
# fits; a sparser one → slightly larger. Kept conservative (±15%) so the template's
# visual identity is preserved — this is fine-tuning around the baseline, not an
# override of it.
DENSITY_MULT = {
    "low": 1.15,    # sparse page: room to enlarge
    "medium": 1.0,  # baseline
    "high": 0.85,   # dense page: shrink to fit more
}

DEFAULT_BASELINE = (26, 18, 14)  # title, card_title, body — matches template_config.json rules.font_hierarchy
DEFAULT_RATIO = 1.8              # title ≈ 1.8x body if the VLM gave no ratio


def autofit(density, ratio, baseline):
    """Return (title, card_title, body) font sizes.

    density  : "low"/"medium"/"high" (unknown/None → "medium")
    ratio    : title/body ratio (e.g. 2.0 = title is 2x body); clamped to [1.3, 3.0]
    baseline : (title, card_title, body) — the template's base sizes
    """
    mult = DENSITY_MULT.get((density or "medium").lower(), 1.0)
    base_title, base_card, base_body = baseline

    # Parse + clamp the title/body ratio (VLM may give None or an outlier).
    try:
        r = float(ratio)
    except (TypeError, ValueError):
        r = DEFAULT_RATIO
    r = max(1.3, min(3.0, r))

    # Anchor on body (most elements are body text), apply the density multiplier,
    # then derive title from the reference's ratio — so the reference's hierarchy
    # is honored, not the hardcoded one.
    body = max(10, round(base_body * mult))
    title = max(body + 2, round(body * r))

    # card_title sits between body and title. Bias it toward the template's
    # original card/body proportion so the template's mid-level feel survives.
    card_prop = (base_card / base_body) if base_body > 0 else 1.29  # e.g. 18/14 ≈ 1.29
    card_title = max(body + 1, round(body * card_prop))
    card_title = min(card_title, title - 2)  # keep hierarchy: card_title < title

    return (title, card_title, body)


def main():
    ap = argparse.ArgumentParser(description="Compute a font-size ramp from density + ratio + baseline.")
    ap.add_argument("--density", default="medium",
                    help="Reference density: low / medium / high (from VLM LAYOUT section)")
    ap.add_argument("--ratio", type=float, default=None,
                    help="title/body ratio (from VLM FORMAT section); default 1.8")
    ap.add_argument("--baseline", default=None,
                    help="Comma-sep title,card_title,body (default 26,18,14)")
    args = ap.parse_args()

    baseline = DEFAULT_BASELINE
    if args.baseline:
        try:
            parts = [int(x.strip()) for x in args.baseline.split(",")]
            if len(parts) == 3:
                baseline = tuple(parts)
            else:
                raise ValueError("expected 3 values")
        except ValueError:
            print(json.dumps({"error": f"bad --baseline (need title,card,body): {args.baseline}"}))
            sys.exit(1)

    title, card, body = autofit(args.density, args.ratio, baseline)
    print(json.dumps({
        "title": title,
        "card_title": card,
        "body": body,
        "density": args.density,
        "ratio": args.ratio if args.ratio is not None else DEFAULT_RATIO,
        "baseline": list(baseline),
    }))


if __name__ == "__main__":
    main()
