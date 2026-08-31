// TopoIcon — the device-class icon set for the topology map
// (NETDEV_IMPORT_AND_FINGERPRINT_SPEC §2.4). Classic network-diagram symbol
// language (the Cisco/eNSP visual vocabulary ops people read at a glance),
// drawn as inline SVG paths — zero third-party icon dependency, and directly
// reusable as React components if the map later moves to React Flow.
//
// Rendered as a NESTED <svg> so it drops into the map's outer <svg> at
// integer coordinates. One visual channel per dimension stays true in here:
// role picks the glyph, the caller picks color (fg vs faint for unmanaged).

export type TopoRole =
  | "router" | "switch" | "firewall" | "ips" | "vpn" | "bastion"
  | "server" | "ap" | "cloud" | "";

const ICONS: Record<TopoRole, React.ReactNode> = {
  // Router: circle with crossed arrows (the universal router glyph).
  router: (
    <>
      <circle cx="12" cy="12" r="8.2" />
      <path d="M6.5 9.2h8.4m0 0-2-2m2 2-2 2" />
      <path d="M17.5 14.8H9.1m0 0 2-2m-2 2 2 2" />
    </>
  ),
  // Switch: rectangle with opposing arrows (core/agg/access share this glyph;
  // the band position already encodes the tier).
  switch: (
    <>
      <rect x="3.5" y="8" width="17" height="8" rx="1.5" />
      <path d="M6.5 10.6h7.5m0 0-1.6-1.4m1.6 1.4-1.6 1.4" />
      <path d="M17.5 13.4h-7.5m0 0 1.6-1.4m-1.6 1.4 1.6 1.4" />
    </>
  ),
  // Firewall: brick wall.
  firewall: (
    <>
      <rect x="3.5" y="5" width="17" height="14" rx="1" />
      <path d="M3.5 9.7h17M3.5 14.3h17" />
      <path d="M9.2 5v4.7M14.8 9.7v4.6M9.2 14.3V19" />
    </>
  ),
  // IPS/IDS: shield with an eye.
  ips: (
    <>
      <path d="M12 3.2 19.5 6v5c0 4.8-3.3 8-7.5 9.8C7.8 19 4.5 15.8 4.5 11V6Z" />
      <circle cx="12" cy="11" r="2.1" />
      <path d="M8.3 11c1-1.6 2.3-2.4 3.7-2.4s2.7.8 3.7 2.4c-1 1.6-2.3 2.4-3.7 2.4S9.3 12.6 8.3 11Z" />
    </>
  ),
  // VPN gateway: padlock.
  vpn: (
    <>
      <rect x="6" y="10.5" width="12" height="9" rx="1.8" />
      <path d="M9 10.5V8a3 3 0 0 1 6 0v2.5" />
      <circle cx="12" cy="15" r="1.1" />
    </>
  ),
  // Bastion: terminal window with a prompt.
  bastion: (
    <>
      <rect x="3.5" y="4.5" width="17" height="15" rx="2" />
      <path d="m7 9.5 3 2.8-3 2.8" />
      <path d="M12.5 15.5H17" />
    </>
  ),
  // Server: rack with slot lines + LEDs.
  server: (
    <>
      <rect x="5.5" y="3.5" width="13" height="17" rx="1.8" />
      <path d="M5.5 9.2h13M5.5 14.8h13" />
      <circle cx="8.4" cy="6.4" r="0.7" />
      <circle cx="8.4" cy="12" r="0.7" />
      <circle cx="8.4" cy="17.6" r="0.7" />
    </>
  ),
  // AP: dot with radiating arcs.
  ap: (
    <>
      <circle cx="12" cy="16.2" r="1.9" />
      <path d="M8.4 12.4a5.2 5.2 0 0 1 7.2 0" />
      <path d="M5.6 9.4a9.2 9.2 0 0 1 12.8 0" />
    </>
  ),
  // Cloud / ISP exit.
  cloud: (
    <path d="M7 18.5h9.6a3.9 3.9 0 0 0 .6-7.8 6.1 6.1 0 0 0-11.7 1.3A4 4 0 0 0 7 18.5Z" />
  ),
  // Unknown: dashed box with a question mark (matches the unmanaged style).
  "": (
    <>
      <rect x="5" y="4.5" width="14" height="15" rx="1.5" strokeDasharray="3 2.4" />
      <path d="M9.9 10.2c.2-1.3 1-2 2.1-2 1.2 0 2.1.9 2.1 2 0 1.6-2.1 1.8-2.1 3.3" fill="none" />
      <circle cx="12" cy="15.6" r="0.9" fill="currentColor" stroke="none" />
    </>
  ),
};

export function TopoIcon({ role, x, y, size = 16, color }: {
  role: string;
  x: number;
  y: number;
  size?: number;
  color?: string;
}): React.JSX.Element {
  const glyph = ICONS[(role || "") as TopoRole] ?? ICONS[""];
  return (
    <svg
      x={x} y={y} width={size} height={size} viewBox="0 0 24 24"
      fill="none" stroke={color ?? "var(--fg)"} strokeWidth={1.6}
      strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"
    >
      {glyph}
    </svg>
  );
}

// Role display labels resolve through the same i18n key family as the rest of
// the topology strings (ndv.topo.role.*); the raw enum never reaches the UI.
export function topoRoleKey(role: string): string {
  return role ? `ndv.topo.role.${role}` : "ndv.topo.role.unknown";
}
