import { style } from "@vanilla-extract/css";
import { vars } from "@/styles/theme.css";

export const wrapper = style({
  display: "flex",
  flexDirection: "column",
  gap: vars.space[2],
});

export const modeGroup = style({
  display: "inline-flex",
  border: `1px solid ${vars.color.borderColor}`,
  borderRadius: vars.radii.md,
  overflow: "hidden",
  width: "fit-content",
});

export const modeButton = style({
  padding: `${vars.space[1]} ${vars.space[3]}`,
  background: "transparent",
  color: vars.color.textSecondary,
  border: "none",
  fontSize: vars.fontSize.sm,
  cursor: "pointer",
  transition: vars.transition.fast,
  ":hover": {
    background: vars.color.hoverBackground,
  },
});

export const modeButtonActive = style({
  background: vars.color.primary,
  color: vars.color.textInverse,
  ":hover": {
    background: vars.color.primary,
  },
});

export const simpleRow = style({
  display: "flex",
  flexWrap: "wrap",
  alignItems: "center",
  gap: vars.space[2],
});

export const select = style({
  padding: `${vars.space[1]} ${vars.space[2]}`,
  background: vars.color.inputBackground,
  color: vars.color.inputText,
  border: `1px solid ${vars.color.inputBorder}`,
  borderRadius: vars.radii.sm,
  fontSize: vars.fontSize.sm,
});

export const explanation = style({
  fontSize: vars.fontSize.sm,
  color: vars.color.textSecondary,
});

export const explanationEmpty = style({
  color: vars.color.textMuted,
  fontStyle: "italic",
});

export const errorText = style({
  fontSize: vars.fontSize.sm,
  color: vars.color.error,
});

export const notice = style({
  fontSize: vars.fontSize.xs,
  color: vars.color.warningText,
  background: vars.color.warningBg,
  padding: `${vars.space[1]} ${vars.space[2]}`,
  borderRadius: vars.radii.sm,
});

export const timezoneLabel = style({
  fontSize: vars.fontSize.xs,
  color: vars.color.textMuted,
});

export const srOnly = style({
  position: "absolute",
  width: "1px",
  height: "1px",
  padding: 0,
  margin: "-1px",
  overflow: "hidden",
  clip: "rect(0, 0, 0, 0)",
  whiteSpace: "nowrap",
  border: 0,
});
