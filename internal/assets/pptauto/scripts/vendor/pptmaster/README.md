# Vendored from ppt-master

Source: https://github.com/hugohe3/ppt-master (skills/ppt-master/scripts)
License: MIT — Copyright (c) 2025-2026 Hugo He. See upstream LICENSE.

Contents: pptx_to_svg/ (PPTX→SVG reverse converter), pptx_shapes/ (preset
geometry registry + OOXML formula evaluation), svg_to_pptx_lib/ (drawingml
utils + native_objects chart/formula modules), hyperlink_contract.py,
pptx_effects.py, language_tags.py.

Local modifications: `from svg_to_pptx.` imports renamed to
`from svg_to_pptx_lib.` to avoid colliding with this skill's own
svg_to_pptx converter (a different lineage with local fixes).
Entry point: ../../pptx_reverse.py
