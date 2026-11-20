# Bundled reading faces

- `XCharter-Roman.otf` — XCharter, Copyright © 2017 Michael Sharpe, distributed
  under the LaTeX Project Public License 1.3c. Unmodified, from CTAN
  (`fonts/xcharter`). It sets the Latin of a book when the serif face is chosen.
- `Inter-Regular.ttf` — Inter, Copyright © 2016 The Inter Project Authors,
  distributed under the SIL Open Font License 1.1. It sets the window's chrome
  (toolbar, folio, panels).

- `DroidSansFallback.ttf` — Droid Sans Fallback, Copyright © 2006-2010 Google
  Corp., distributed under the Apache License 2.0. It is the last face of the
  set: phones give an app no path to the system's fonts, so without it a book
  has nothing to measure Chinese, Japanese and Korean with. See
  `LICENSE-DroidSansFallback.txt`.

The system's CJK face is still loaded at runtime and follows both in the same
family, so Chinese and Japanese keep their system shapes.
