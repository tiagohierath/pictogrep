# Pictogrep UI blueprint

The rules every screen follows. Read this before adding a size, a spacing
value, a radius, a button style, or a component.

## What is already settled

The home page and the folders page are done. Their design is approved and
should be preserved. Change them only when asked directly, not as a side
effect of work elsewhere.

## 1. Spacing

Only these values:

- 4px: tiny gaps
- 8px: normal internal gaps
- 12px: compact padding
- 16px: normal padding
- 24px: section spacing
- 32px: large separation

Never invent values like 13px, 18px, or 22px.

## 2. Button sizes

Only three heights:

- Small: 28px
- Normal: 36px
- Large: 44px

The default button is 36px tall, 12px horizontal padding, 16px icon, 8px gap
between icon and text.

Buttons sitting beside each other always share a height.

## 3. Icon buttons

Only 28x28px (compact), 36x36px (normal), 44x44px (important or mobile).

Icons inside are 16px normally, 20px for large controls. Never shrink the
clickable area down to the icon itself.

## 4. Inputs

Normal inputs are 36px tall with 8px 12px padding. Search bars may be 40px or
44px. Inputs and the buttons next to them align vertically.

## 5. Border radius

Only 4px (tiny elements), 8px (buttons, inputs), 12px (cards), 16px (large
panels and modals). No random variation.

## 6. Typography

- Page title: 24px
- Section title: 18px
- Normal text: 14px
- Secondary text: 12px

Weights: regular, medium, bold. That is all three of them. Do not solve
hierarchy by reaching for another font size.

## 7. Layout

Every page gets 16px to 24px of page padding. Cap content width only where it
helps; image grids can fill the viewport.

Prefer clear alignment, repeated grid sizes, consistent edges, and fewer
nested boxes. Avoid a card inside a card inside a card.

## 8. Image grid

The images are the main content, so the UI stays quieter than they are.

- One gap everywhere, usually 8px
- Thumbnail aspect ratio does not change from one place to another
- Controls appear the same way on hover and selection
- One consistent selected state

## 9. States

Every interactive control defines default, hover, active, focus, and disabled.
Selectable elements also define selected. Do not invent a different
interaction language per component.

## 10. Visual priority

Classify before styling: primary (the main action on the screen), secondary
(useful, not dominant), tertiary (small actions, metadata, menus). Usually one
obvious primary action per area.

## 11. Density

Pictogrep is a desktop creative tool, so the UI is compact. Do not make things
giant because modern web apps do. Defaults: 36px controls, 14px text, 8px
gaps, 16px page padding.

## 12. The main rule

Before creating a new size, spacing value, radius, button style, or component,
reuse an existing one. If a component looks similar to another component, it
probably follows the same rules.

Consistency beats cleverness.
