# Layout review

Captured the running application in Chromium with synthetic sessions, long Japanese/English labels and a Japanese locale. Reviewed widths of 1920, 1440, 1024, 768, 390 and 320px, plus a 1024×520 short window. The 56 captures cover list, search options, time picker, long project heading, Skills, session detail, inspector, original JSON and archive states. API loading was delayed for one capture to inspect the actual loading state.

| Finding | Correction | Before / after |
| --- | --- | --- |
| At 1920px, navigation began at x=28, the list at x=88 and details at x=188. On mobile, navigation began at x=10 and content at x=14. | Shared maximum width and responsive gutter variables align all page origins: x=88 at 1920px, x=14 at 390px. | [Wide before](layout-review/sessions-1920-before.png) / [after](layout-review/sessions-1920-after.png) |
| Project and result headings occupied different-height rows. | Match the desktop heading row heights; keep compact mobile headings. Mobile time popovers and dialogs also use the page gutter instead of conflicting browser defaults. | Same wide captures |
| A mobile list row placed its agent on a separate row with an empty left cell. | Place the agent beside the title. The measured first row shrank from 83px to 64px while retaining its title, ID and agent. | [Mobile before](layout-review/sessions-390-before.png) / [after](layout-review/sessions-390-after.png) |
| At 1024×900, the inspector extended to y=1186, outside the viewport. Loading text touched its border and tabs used a different inset. | Fit the panel to the actual available viewport on open, resize and scroll; keep the bottom at y=888 with a 12px clearance. Match header/body/tab insets and pad loading/error text. | [Inspector before](layout-review/inspector-1024-before.png) / [after](layout-review/inspector-1024-after.png) |
| Wrapped skill names were cramped; multiline headers and numeric cells lacked consistent alignment. | Increase wrapped-text line height, bottom-align column headers and right-align numeric values. Mobile labels retain left alignment. | Covered by 768px and mobile audit captures |
| Archive size columns reserved unnecessary width, Japanese dates split before the final 日, and confirmation input/button dimensions differed. | Size byte columns to their content, right-align their text, keep dates whole, and use a shared 32px control height. Confirmation input fills its mobile column ([download confirmation](layout-review/confirmation-after.png)). Remove empty selection-label spacing. | [Archive before](layout-review/archive-320-before.png) / [after](layout-review/archive-320-after.png) |

## Reproduce

```sh
AGENTLOG_BIN=./agentlog AGENTLOG_SCREENSHOT_DIR=/tmp/layout-review python3 scripts/layout_browser_test.py
```

The script writes screenshots and `measurements.json`. It checks shared page axes, actual numeric/byte text edges, mobile row placement, document overflow, panel bounds and header stability while scrolling. `AGENTLOG_LAYOUT_CHECKS=0` allows capture of older versions that intentionally fail the new alignment checks.

Existing browser checks cover tool-pair navigation, fixed inspector headers/mobile focus behavior, archive ZIP/download confirmation/deletion/replay suppression, shared search, Skills and project/timeline navigation. Layout work adds no data migration, network polling or application dependency.
