import { style } from "@vanilla-extract/css";
import { vars } from "../../styles/theme.css";

export const container = style({
  display: "flex",
  flexDirection: "column",
  gap: "6px",
  padding: "10px 12px",
  background: vars.color.hoverBackground,
  border: `1px solid ${vars.color.borderColor}`,
  borderRadius: "6px",
  fontSize: "13px",
});

export const row = style({
  display: "flex",
  alignItems: "center",
  gap: "8px",
});

export const label = style({
  color: vars.color.textMuted,
  fontSize: "12px",
  minWidth: "52px",
  flexShrink: 0,
});

export const branch = style({
  fontFamily: vars.font.mono,
  color: vars.color.primary,
  fontWeight: "500",
});

export const shipped = style({
  color: vars.color.success,
  fontWeight: "500",
});

export const notShipped = style({
  color: vars.color.warning,
  fontWeight: "500",
});

export const detail = style({
  fontSize: "12px",
  color: vars.color.textSecondary,
});

export const commitSha = style({
  fontFamily: vars.font.mono,
  fontSize: "11px",
  color: vars.color.textMuted,
});

export const prLink = style({
  color: vars.color.primary,
  textDecoration: "none",
  selectors: {
    "&:hover": { textDecoration: "underline" },
  },
});
