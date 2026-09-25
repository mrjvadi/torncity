// Line icons, drawn inline (no icon font, nothing fetched). 24×24 grid,
// stroke in the text colour.

const PATHS: Record<string, string> = {
  home: 'M3 11l9-7 9 7M5 10v10h5v-6h4v6h5V10',
  users: 'M16 19v-1a4 4 0 00-4-4H6a4 4 0 00-4 4v1M9 10a3.5 3.5 0 100-7 3.5 3.5 0 000 7M22 19v-1a4 4 0 00-3-3.8M16 3.2a3.5 3.5 0 010 6.6',
  user: 'M20 21v-2a4 4 0 00-4-4H8a4 4 0 00-4 4v2M12 11a4 4 0 100-8 4 4 0 000 8',
  briefcase: 'M3 7h18v13H3zM8 7V5a2 2 0 012-2h4a2 2 0 012 2v2M3 13h18',
  city: 'M3 21h18M5 21V8l5-3v16M10 21V3l9 4v14M13 9h3M13 13h3M13 17h3M7 11h1M7 15h1',
  globe: 'M12 21a9 9 0 100-18 9 9 0 000 18M3 12h18M12 3a14 14 0 010 18M12 3a14 14 0 000 18',
  shield: 'M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z',
  sword: 'M14.5 17.5L3 6V3h3l11.5 11.5M13 19l6-6M16 16l4 4M19 21l2-2',
  scale: 'M12 3v18M5 21h14M6 7h12M3 13l3-6 3 6a3 3 0 01-6 0M15 13l3-6 3 6a3 3 0 01-6 0',
  vote: 'M9 12l2 2 4-4M4 20h16M6 16V4h12v12',
  chart: 'M3 3v18h18M7 15l4-4 3 3 6-7',
  coins: 'M9 8a6 3 0 1012 0A6 3 0 009 8M9 8v4c0 1.7 2.7 3 6 3s6-1.3 6-3V8M3 12a6 3 0 006 3M3 12v4c0 1.7 2.7 3 6 3',
  bank: 'M3 21h18M4 10h16M12 3l9 5H3zM6 10v8M10 10v8M14 10v8M18 10v8',
  store: 'M3 9l2-5h14l2 5M4 9v11h16V9M9 20v-6h6v6M3 9a3 3 0 006 0 3 3 0 006 0 3 3 0 006 0',
  handcuffs: 'M7 13a4 4 0 100 8 4 4 0 000-8M17 13a4 4 0 100 8 4 4 0 000-8M7 13V8a2 2 0 012-2M17 13V8a2 2 0 00-2-2M9 6h6',
  heart: 'M20.8 5.6a5.5 5.5 0 00-7.8 0L12 6.6l-1-1a5.5 5.5 0 00-7.8 7.8l1 1L12 22l7.8-7.6 1-1a5.5 5.5 0 000-7.8z',
  target: 'M12 21a9 9 0 100-18 9 9 0 000 18M12 16a4 4 0 100-8 4 4 0 000 8M12 12h.01',
  trophy: 'M8 21h8M12 17v4M7 4h10v5a5 5 0 01-10 0zM7 6H4a3 3 0 003 4M17 6h3a3 3 0 01-3 4',
  flag: 'M4 22V4M4 4h13l-2 4 2 4H4',
  eye: 'M1 12s4-7 11-7 11 7 11 7-4 7-11 7S1 12 1 12M12 15a3 3 0 100-6 3 3 0 000 6',
  file: 'M14 3H6v18h12V7zM14 3v4h4M9 13h6M9 17h6',
  message: 'M21 12a8 8 0 01-12 7l-5 1 1-4a8 8 0 1116-4',
  activity: 'M22 12h-4l-3 9L9 3l-3 9H2',
  list: 'M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01',
  key: 'M15 7a4 4 0 11-4 4M11 11L3 19v2h3v-2h2v-2h2l1-1',
  search: 'M11 19a8 8 0 100-16 8 8 0 000 16M21 21l-4.3-4.3',
  menu: 'M3 6h18M3 12h18M3 18h18',
  x: 'M18 6L6 18M6 6l12 12',
  chevron: 'M9 18l6-6-6-6',
  back: 'M15 18l-6-6 6-6',
  download: 'M12 3v12M7 10l5 5 5-5M4 21h16',
  columns: 'M3 4h18v16H3zM9 4v16M15 4v16',
  refresh: 'M21 12a9 9 0 01-15 6.7L3 16M3 12a9 9 0 0115-6.7L21 8M21 3v5h-5M3 21v-5h5',
  sun: 'M12 17a5 5 0 100-10 5 5 0 000 10M12 1v2M12 21v2M4.2 4.2l1.4 1.4M18.4 18.4l1.4 1.4M1 12h2M21 12h2M4.2 19.8l1.4-1.4M18.4 5.6l1.4-1.4',
  moon: 'M21 12.8A9 9 0 1111.2 3a7 7 0 009.8 9.8',
  contrast: 'M12 21a9 9 0 100-18 9 9 0 000 18M12 3v18',
  filter: 'M3 4h18l-7 8v6l-4 2v-8z',
  logout: 'M9 21H5a2 2 0 01-2-2V5a2 2 0 012-2h4M16 17l5-5-5-5M21 12H9',
  bell: 'M18 8a6 6 0 10-12 0c0 7-3 9-3 9h18s-3-2-3-9M13.7 21a2 2 0 01-3.4 0',
  gavel: 'M14 13l-7.5 7.5a2.1 2.1 0 01-3-3L11 10M16 16l6-6M8 8l6-6M9 7l8 8',
  box: 'M21 8l-9-5-9 5 9 5zM3 8v8l9 5 9-5V8M12 13v8',
  server: 'M3 4h18v7H3zM3 13h18v7H3zM7 7.5h.01M7 16.5h.01',
  language: 'M3 5h12M9 3v2M11 5c-1.5 5-4 8-7 9M6 9c1.5 3 4 5 7 5M13 21l4-9 4 9M14.5 18h5',
  plus: 'M12 5v14M5 12h14',
  check: 'M20 6L9 17l-5-5',
  alert: 'M12 9v4M12 17h.01M10.3 3.9L1.8 18a2 2 0 001.7 3h17a2 2 0 001.7-3L13.7 3.9a2 2 0 00-3.4 0',
  info: 'M12 21a9 9 0 100-18 9 9 0 000 18M12 16v-4M12 8h.01',
  copy: 'M9 9h11v11H9zM5 15H4V4h11v1',
  mute: 'M11 5L6 9H2v6h4l5 4zM23 9l-6 6M17 9l6 6',
  ban: 'M12 21a9 9 0 100-18 9 9 0 000 18M5.6 5.6l12.8 12.8',
  clock: 'M12 21a9 9 0 100-18 9 9 0 000 18M12 7v5l3 3',
};

export type IconName = keyof typeof PATHS;

export function Icon({ name, size = 18, className }: { name: IconName | string; size?: number; className?: string }) {
  const d = PATHS[name] ?? PATHS.info;
  return (
    <svg
      className={`icon ${className ?? ''}`}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      <path d={d} />
    </svg>
  );
}
