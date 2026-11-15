# UI colors and icons

White panels, neutral text and thin gray borders restore the original layout. Blue is the single interaction accent for links, active filters, selection and focus. Red and green are reserved for failure/success status; event categories do not receive colors. Toolbar padding is compact and symmetric. Subagent links use ordinary blue text without filled badges; the inspector and tool input/output previews keep white headers.

![Neutral panels and fixed scroll context](ui-palette-scroll.png)

`internal/app/static/palette.css` defines the shared semantic CSS variables. Event cards, the inspector header and subagent entries each have one small monochrome icon from [Lucide](https://lucide.dev). The original SVGs are bundled in `internal/app/static/icons`; the pinned revision and ISC/MIT notices are in `THIRD_PARTY_NOTICES.txt`. Paired tool input/output sections do not add another icon inside the inspector. No icon package, CDN or runtime network dependency is required.

Sticky navigation, list headings, timeline filters and fixed inspector controls remain. `--appbar-height` and `--timeline-toolbar-height` keep scroll targets and panels below the controls, including when filters wrap. Details use the full-screen inspector on mobile.

```sh
AGENTLOG_BIN=./agentlog python3 scripts/palette_browser_test.py
```

The check covers neutral surfaces, icons, text contrast, shared accent variables, scrolling and 768/390/320px widths. Screenshots use synthetic data.
