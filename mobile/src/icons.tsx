// One coherent icon set: inline stroke SVGs on a 24px grid, thin strokes,
// round caps — the light-line look of the reference app. Everything scales
// with the `size` prop and inherits currentColor.
import type { SVGProps } from 'react'

function base(props: { size?: number } & SVGProps<SVGSVGElement>) {
  const { size = 24, children, ...rest } = props
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.4}
      strokeLinecap="round"
      strokeLinejoin="round"
      {...rest}
    >
      {children}
    </svg>
  )
}

type P = { size?: number } & SVGProps<SVGSVGElement>

export const IcMenu = (p: P) => base({ ...p, children: <><path d="M4 7h16" /><path d="M4 12h16" /><path d="M4 17h16" /></> })

export const IcChevronLeft = (p: P) => base({ ...p, children: <path d="M14.5 5.5 8 12l6.5 6.5" /> })

export const IcPlus = (p: P) => base({ ...p, children: <><path d="M12 5v14" /><path d="M5 12h14" /></> })

export const IcCalendar = (p: P) =>
  base({ ...p, children: <><rect x="4" y="5.5" width="16" height="15" rx="2.5" /><path d="M4 10h16" /><path d="M8.5 3.5v4" /><path d="M15.5 3.5v4" /></> })

export const IcRun = (p: P) =>
  base({ ...p, children: <path d="M8 5.5v13l10-6.5-10-6.5Z" /> })

export const IcSearch = (p: P) =>
  base({ ...p, children: <><circle cx="11" cy="11" r="6.5" /><path d="m16 16 4.5 4.5" /></> })

export const IcKebab = (p: P) =>
  base({ ...p, children: <><circle cx="12" cy="5" r="1.3" fill="currentColor" stroke="none" /><circle cx="12" cy="12" r="1.3" fill="currentColor" stroke="none" /><circle cx="12" cy="19" r="1.3" fill="currentColor" stroke="none" /></> })

export const IcHome = (p: P) =>
  base({ ...p, children: <><path d="m4 11 8-7 8 7" /><path d="M6 9.5V20h12V9.5" /><path d="M10 20v-5h4v5" /></> })

export const IcOutdent = (p: P) =>
  base({ ...p, children: <><path d="M4.5 4v16" /><path d="M20 12H8.5" /><path d="m12.5 8-4 4 4 4" /></> })

export const IcIndent = (p: P) =>
  base({ ...p, children: <><path d="M19.5 4v16" /><path d="M4 12h11.5" /><path d="m11.5 8 4 4-4 4" /></> })

export const IcUndo = (p: P) =>
  base({ ...p, children: <><path d="M4.5 9.5h9.5a5.5 5.5 0 0 1 0 11H10" /><path d="M8.5 5.5l-4 4 4 4" /></> })

export const IcRedo = (p: P) =>
  base({ ...p, children: <><path d="M19.5 9.5H10a5.5 5.5 0 0 0 0 11h4" /><path d="m15.5 5.5 4 4-4 4" /></> })

export const IcCheck = (p: P) => base({ ...p, children: <path d="m5 13 4.5 4.5L19 7" /> })

export const IcPencil = (p: P) =>
  base({ ...p, children: <><path d="m5 19 .9-3.6L16.6 4.7a1.8 1.8 0 0 1 2.6 0l.1.1a1.8 1.8 0 0 1 0 2.6L8.6 18.1 5 19Z" /><path d="m14.5 6.5 3 3" /></> })

export const IcAt = (p: P) =>
  base({ ...p, children: <><circle cx="12" cy="12" r="3.6" /><path d="M15.6 8.5v4.9a2.4 2.4 0 0 0 4.8 0V12a8.4 8.4 0 1 0-3.3 6.7" /></> })

export const IcCode = (p: P) =>
  base({ ...p, children: <><path d="m8.5 8-4 4 4 4" /><path d="m15.5 8 4 4-4 4" /></> })

export const IcKeyboardDown = (p: P) =>
  base({ ...p, children: <><rect x="3.5" y="3.5" width="17" height="11" rx="2" /><path d="M6.5 6.8h.01M10 6.8h.01M13.5 6.8h.01M17 6.8h.01M6.5 9.6h.01M10 9.6h.01M13.5 9.6h.01M17 9.6h.01" strokeWidth="1.7" /><path d="M8 11.8h8" /><path d="m9.5 18.5 2.5 2.5 2.5-2.5" /></> })

export const IcArrowLeft = (p: P) =>
  base({ ...p, children: <><path d="M20 12H4.5" /><path d="m10.5 6-6 6 6 6" /></> })

export const IcX = (p: P) => base({ ...p, children: <><path d="m6 6 12 12" /><path d="M18 6 6 18" /></> })

export const IcSwap = (p: P) =>
  base({ ...p, children: <><path d="M4 8.5h13" /><path d="m13.5 4.5 4 4-4 4" /><path d="M20 15.5H7" /><path d="m10.5 11.5-4 4 4 4" /></> })

export const IcStar = (p: P) =>
  base({ ...p, children: <path d="m12 4 2.3 4.9 5.2.7-3.8 3.7.9 5.2L12 16l-4.6 2.5.9-5.2L4.5 9.6l5.2-.7L12 4Z" /> })

export const IcStarFilled = (p: P) =>
  base({ ...p, children: <path d="m12 4 2.3 4.9 5.2.7-3.8 3.7.9 5.2L12 16l-4.6 2.5.9-5.2L4.5 9.6l5.2-.7L12 4Z" fill="currentColor" /> })

export const IcExport = (p: P) =>
  base({ ...p, children: <><path d="M12 14.5V4" /><path d="m7.5 8.5 4.5-4.5 4.5 4.5" /><path d="M5 14v5.5h14V14" /></> })

export const IcExpand = (p: P) =>
  base({ ...p, children: <><path d="m20 4-5.5 5.5" /><path d="M20 8.5V4h-4.5" /><path d="m4 20 5.5-5.5" /><path d="M4 15.5V20h4.5" /></> })

export const IcCollapse = (p: P) =>
  base({ ...p, children: <><path d="m14 10 6-6" /><path d="M14 5.5V10h4.5" /><path d="m10 14-6 6" /><path d="M10 18.5V14H5.5" /></> })

export const IcTrash = (p: P) =>
  base({ ...p, children: <><path d="M4.5 7h15" /><path d="M9.5 7V4.8h5V7" /><path d="M6.5 7l1 13h9l1-13" /><path d="M10.2 10.5v6M13.8 10.5v6" /></> })

export const IcChevronDown = (p: P) => base({ ...p, children: <path d="m6 9.5 6 6 6-6" /> })

export const IcMoveTo = (p: P) =>
  base({ ...p, children: <><path d="M4 12h15" /><path d="m13.5 6 6 6-6 6" /></> })

export const IcDuplicate = (p: P) =>
  base({ ...p, children: <><rect x="8.5" y="8.5" width="11" height="11" rx="2" /><path d="M15.5 5.5v-1a2 2 0 0 0-2-2h-8a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h1" /></> })

export const IcDiamond = (p: P) =>
  base({ ...p, children: <path d="M12 3.5 20.5 12 12 20.5 3.5 12 12 3.5Z" /> })

export const IcHash = (p: P) =>
  base({ ...p, children: <><path d="M9.5 4 8 20" /><path d="M16 4l-1.5 16" /><path d="M4.5 9h16" /><path d="M3.5 15h16" /></> })

export const IcBullets = (p: P) =>
  base({ ...p, children: <><circle cx="5.5" cy="6" r="1.1" fill="currentColor" stroke="none" /><circle cx="5.5" cy="12" r="1.1" fill="currentColor" stroke="none" /><circle cx="5.5" cy="18" r="1.1" fill="currentColor" stroke="none" /><path d="M10 6h10M10 12h10M10 18h10" /></> })

export const IcTodoList = (p: P) =>
  base({ ...p, children: <><rect x="3.5" y="4" width="5" height="5" rx="1" /><path d="m4.8 6.5 1.1 1.1 2-2.2" /><rect x="3.5" y="14" width="5" height="5" rx="1" /><path d="M12 6.5h8.5M12 16.5h8.5" /></> })

export const IcQuoteBlock = (p: P) =>
  base({ ...p, children: <><path d="M4.5 5v14" /><path d="M9 7h11M9 12h11M9 17h7" /></> })

export const IcCodeBlock = (p: P) =>
  base({ ...p, children: <><rect x="3.5" y="4.5" width="17" height="15" rx="2" /><path d="m9.5 9.5-2.5 2.5 2.5 2.5" /><path d="m14.5 9.5 2.5 2.5-2.5 2.5" /></> })

export const IcLogList = (p: P) =>
  base({ ...p, children: <><path d="M4 6h16M4 12h16M4 18h11" /></> })

export const IcNote = (p: P) =>
  base({ ...p, children: <><path d="M5 4.5h14v10l-5 5H5v-15Z" /><path d="M14 19.5v-5h5" /></> })

export const IcGear = (p: P) =>
  base({ ...p, children: <><circle cx="12" cy="12" r="3" /><path d="M12 2.8v2.4M12 18.8v2.4M2.8 12h2.4M18.8 12h2.4M5.5 5.5l1.7 1.7M16.8 16.8l1.7 1.7M18.5 5.5l-1.7 1.7M7.2 16.8l-1.7 1.7" /></> })
