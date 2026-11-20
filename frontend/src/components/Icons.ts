/**
 * Inline SVG icon library (stroke-based, 24x24 viewBox).
 * Every icon is a functional component accepting an optional `size` prop.
 */
import { defineComponent, h, type VNode } from "vue";

const STROKE_ATTRS = {
  viewBox: "0 0 24 24",
  fill: "none",
  stroke: "currentColor",
  "stroke-width": 1.7,
  "stroke-linecap": "round",
  "stroke-linejoin": "round",
  "aria-hidden": "true",
} as const;

function icon(name: string, children: () => VNode[]) {
  return defineComponent({
    name,
    props: {
      size: { type: Number, default: 18 },
    },
    setup(props) {
      return () =>
        h(
          "svg",
          {
            width: props.size,
            height: props.size,
            ...STROKE_ATTRS,
          },
          children(),
        );
    },
  });
}

const p = (d: string): VNode => h("path", { d });
const c = (cx: number, cy: number, r: number): VNode => h("circle", { cx, cy, r });

export const IconDashboard = icon("IconDashboard", () => [
  p("M3 3h8v8H3z"),
  p("M13 3h8v5h-8z"),
  p("M13 10h8v11h-8z"),
  p("M3 13h8v8H3z"),
]);

export const IconProfiles = icon("IconProfiles", () => [
  p("M12 3 3 8l9 5 9-5-9-5z"),
  p("M3 12.5l9 5 9-5"),
  p("M3 17l9 5 9-5"),
]);

export const IconTest = icon("IconTest", () => [
  p("M9 3h6"),
  p("M10 3v5.2L4.9 17a2 2 0 0 0 1.8 3h10.6a2 2 0 0 0 1.8-3L14 8.2V3"),
  p("M7.5 14h9"),
]);

export const IconConnections = icon("IconConnections", () => [
  c(5, 12, 2.4),
  c(19, 5.5, 2.4),
  c(19, 18.5, 2.4),
  p("M7.2 11 16.8 6.3"),
  p("M7.2 13l9.5 4.7"),
]);

export const IconLogs = icon("IconLogs", () => [
  p("M6 3h8.5L20 8.5V21H6z"),
  p("M14 3v6h6"),
  p("M9.5 13h6"),
  p("M9.5 17h6"),
]);

export const IconSettings = icon("IconSettings", () => [
  c(12, 12, 3),
  p(
    "M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09a1.65 1.65 0 0 0 1.51-1 1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33h.01a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82v.01a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z",
  ),
]);

export const IconAbout = icon("IconAbout", () => [c(12, 12, 9), p("M12 16.5v-5"), p("M12 8h.01")]);

export const IconRefresh = icon("IconRefresh", () => [
  p("M21 12a9 9 0 1 1-2.64-6.36"),
  p("M21 3v6h-6"),
]);

export const IconDownload = icon("IconDownload", () => [
  p("M12 3v11"),
  p("M8 10.5l4 4 4-4"),
  p("M4 20.5h16"),
]);

export const IconUpload = icon("IconUpload", () => [
  p("M12 14V3"),
  p("M8 6.5l4-4 4 4"),
  p("M4 20.5h16"),
]);

export const IconClose = icon("IconClose", () => [p("M6 6l12 12"), p("M18 6 6 18")]);

export const IconCheck = icon("IconCheck", () => [p("M4.5 12.5 9.5 17.5 19.5 6.5")]);

export const IconTrash = icon("IconTrash", () => [
  p("M4 7h16"),
  p("M9.5 7V4.5h5V7"),
  p("M6.5 7l.8 13.5h9.4L17.5 7"),
  p("M10 11v6"),
  p("M14 11v6"),
]);

export const IconEdit = icon("IconEdit", () => [
  p("M16.8 3.2a2.1 2.1 0 0 1 3 3L7.5 18.5 3 20l1.5-4.5L16.8 3.2z"),
]);

export const IconPlus = icon("IconPlus", () => [p("M12 5v14"), p("M5 12h14")]);

export const IconFile = icon("IconFile", () => [
  p("M6 2.5h8.5L20 8v13.5H6z"),
  p("M14 2.5V8h6"),
  p("M9.5 13h5"),
  p("M9.5 16.5h5"),
]);

export const IconClipboard = icon("IconClipboard", () => [
  p("M9.5 3h5v3h-5z"),
  p("M9.5 4.5H7a1.5 1.5 0 0 0-1.5 1.5v14A1.5 1.5 0 0 0 7 21.5h10a1.5 1.5 0 0 0 1.5-1.5V6a1.5 1.5 0 0 0-1.5-1.5h-2.5"),
]);

export const IconLink = icon("IconLink", () => [
  p("M10 13.5a4.5 4.5 0 0 0 6.4.4l2.8-2.8a4.5 4.5 0 0 0-6.4-6.4l-1.6 1.6"),
  p("M14 10.5a4.5 4.5 0 0 0-6.4-.4l-2.8 2.8a4.5 4.5 0 0 0 6.4 6.4l1.6-1.6"),
]);

export const IconBolt = icon("IconBolt", () => [
  h("path", {
    d: "M13.2 2 4.3 13.4h6.1L9.1 22l9.2-11.6h-6.3L13.2 2z",
    fill: "currentColor",
    stroke: "none",
  }),
]);

export const IconPower = icon("IconPower", () => [
  p("M12 2.5v9"),
  p("M17.7 6.3a8 8 0 1 1-11.4 0"),
]);

export const IconGauge = icon("IconGauge", () => [
  p("M5 17.5a8.5 8.5 0 1 1 14 0"),
  p("M12 13.5l3.5-4"),
  c(12, 13.5, 1.2),
]);

export const IconGlobe = icon("IconGlobe", () => [
  c(12, 12, 9),
  p("M3 12h18"),
  p("M12 3a14 14 0 0 1 0 18 14 14 0 0 1 0-18"),
]);

export const IconShield = icon("IconShield", () => [
  p("M12 2.5 4.5 5.5v5.7c0 4.7 3.1 8.7 7.5 10.3 4.4-1.6 7.5-5.6 7.5-10.3V5.5L12 2.5z"),
]);

export const IconClock = icon("IconClock", () => [c(12, 12, 9), p("M12 7v5l3.2 2")]);

export const IconWarn = icon("IconWarn", () => [
  p("M12 3.5 2.5 20h19L12 3.5z"),
  p("M12 10v4.5"),
  p("M12 17.5h.01"),
]);

export const IconFolder = icon("IconFolder", () => [
  p("M3 6a1.5 1.5 0 0 1 1.5-1.5h4.6l2 2.6h8.4A1.5 1.5 0 0 1 21 8.6V18a1.5 1.5 0 0 1-1.5 1.5h-15A1.5 1.5 0 0 1 3 18V6z"),
]);

export const IconExternal = icon("IconExternal", () => [
  p("M14 4h6v6"),
  p("M20 4 11 13"),
  p("M19.5 14v5a1.5 1.5 0 0 1-1.5 1.5H5.5A1.5 1.5 0 0 1 4 19V6.5A1.5 1.5 0 0 1 5.5 5h5"),
]);

export const IconChevron = icon("IconChevron", () => [p("M9 6l6 6-6 6")]);
