#!/usr/bin/env python3
"""Generate the Material 3 colour roles for web/m3.css.

Material 3 builds every colour in a scheme from one seed. The seed becomes five
tonal palettes (primary, secondary, tertiary, neutral, neutral variant) plus a
fixed error palette, each sampled at tones 0 to 100, and every named role in the
spec is one of those samples. Light and dark are the same palettes read at
different tones, which is why an M3 scheme stays coherent in both.

A tone is CIE L*, exactly as in Google's HCT, so tones here are the real thing.
Hue and chroma are taken in CIELCh rather than CAM16, which is a close enough
stand-in to be indistinguishable in a UI and needs no colour appearance model.
Chroma is reduced per tone until the colour fits in sRGB, so nothing clips.

Run it after changing SEED and paste the output over the token block in
web/m3.css. It is checked in so the palette can be regenerated rather than
hand-edited, because hand-edited tonal palettes stop being tonal palettes.
"""

import math

# Navy, for navylily.tv. Picture libraries want the chrome to stay out of the
# way, and an M3 scheme built on a dark blue keeps surfaces close to neutral
# while the accents stay recognisably ours.
SEED = "#1F4788"


# --- colour conversion, sRGB <-> CIELAB ------------------------------------

def srgb_to_linear(c):
    return c / 12.92 if c <= 0.04045 else ((c + 0.055) / 1.055) ** 2.4


def linear_to_srgb(c):
    return c * 12.92 if c <= 0.0031308 else 1.055 * (c ** (1 / 2.4)) - 0.055


WHITE = (0.95047, 1.0, 1.08883)


def hex_to_lab(value):
    r, g, b = (int(value[i:i + 2], 16) / 255 for i in (1, 3, 5))
    r, g, b = (srgb_to_linear(c) for c in (r, g, b))
    x = (0.4124564 * r + 0.3575761 * g + 0.1804375 * b) / WHITE[0]
    y = (0.2126729 * r + 0.7151522 * g + 0.0721750 * b) / WHITE[1]
    z = (0.0193339 * r + 0.1191920 * g + 0.9503041 * b) / WHITE[2]
    f = [c ** (1 / 3) if c > 216 / 24389 else (24389 / 27 * c + 16) / 116 for c in (x, y, z)]
    return 116 * f[1] - 16, 500 * (f[0] - f[1]), 200 * (f[1] - f[2])


def lab_to_rgb(lightness, a, b):
    """Returns the three channels as floats, which may fall outside 0..1."""
    fy = (lightness + 16) / 116
    fx, fz = fy + a / 500, fy - b / 200

    def undo(t):
        return t ** 3 if t ** 3 > 216 / 24389 else (116 * t - 16) * 27 / 24389

    x, y, z = undo(fx) * WHITE[0], undo(fy) * WHITE[1], undo(fz) * WHITE[2]
    return (
        linear_to_srgb(3.2404542 * x - 1.5371385 * y - 0.4985314 * z),
        linear_to_srgb(-0.9692660 * x + 1.8760108 * y + 0.0415560 * z),
        linear_to_srgb(0.0556434 * x - 0.2040259 * y + 1.0572252 * z),
    )


def tone(hue, chroma, lightness):
    """One sample of a tonal palette, with chroma pulled in until sRGB holds it."""
    radians = math.radians(hue)
    while chroma > 0:
        rgb = lab_to_rgb(lightness, chroma * math.cos(radians), chroma * math.sin(radians))
        if all(-0.0001 <= c <= 1.0001 for c in rgb):
            return "#" + "".join(f"{round(min(max(c, 0), 1) * 255):02X}" for c in rgb)
        chroma -= 0.5
    grey = round(min(max(linear_to_srgb(((lightness + 16) / 116) ** 3), 0), 1) * 255)
    return "#" + f"{grey:02X}" * 3


# --- the five palettes, as the tonal spot scheme derives them ---------------

_, seed_a, seed_b = hex_to_lab(SEED)
SEED_HUE = math.degrees(math.atan2(seed_b, seed_a)) % 360
SEED_CHROMA = math.hypot(seed_a, seed_b)

PALETTES = {
    "primary": (SEED_HUE, max(SEED_CHROMA, 48)),
    "secondary": (SEED_HUE, 16),
    "tertiary": (SEED_HUE + 60, 24),
    "neutral": (SEED_HUE, 6),
    "neutralvariant": (SEED_HUE, 8),
    "error": (25.0, 84),
}


def at(palette, lightness):
    hue, chroma = PALETTES[palette]
    return tone(hue, chroma, lightness)


# --- role to tone, straight out of the spec ---------------------------------

ROLES = [
    # role,                     light,                     dark
    ("primary", ("primary", 40), ("primary", 80)),
    ("on-primary", ("primary", 100), ("primary", 20)),
    ("primary-container", ("primary", 90), ("primary", 30)),
    ("on-primary-container", ("primary", 10), ("primary", 90)),
    ("secondary", ("secondary", 40), ("secondary", 80)),
    ("on-secondary", ("secondary", 100), ("secondary", 20)),
    ("secondary-container", ("secondary", 90), ("secondary", 30)),
    ("on-secondary-container", ("secondary", 10), ("secondary", 90)),
    ("tertiary", ("tertiary", 40), ("tertiary", 80)),
    ("on-tertiary", ("tertiary", 100), ("tertiary", 20)),
    ("tertiary-container", ("tertiary", 90), ("tertiary", 30)),
    ("on-tertiary-container", ("tertiary", 10), ("tertiary", 90)),
    ("error", ("error", 40), ("error", 80)),
    ("on-error", ("error", 100), ("error", 20)),
    ("error-container", ("error", 90), ("error", 30)),
    ("on-error-container", ("error", 10), ("error", 90)),
    ("surface", ("neutral", 98), ("neutral", 6)),
    ("on-surface", ("neutral", 10), ("neutral", 90)),
    ("surface-dim", ("neutral", 87), ("neutral", 6)),
    ("surface-bright", ("neutral", 98), ("neutral", 24)),
    ("surface-container-lowest", ("neutral", 100), ("neutral", 4)),
    ("surface-container-low", ("neutral", 96), ("neutral", 10)),
    ("surface-container", ("neutral", 94), ("neutral", 12)),
    ("surface-container-high", ("neutral", 92), ("neutral", 17)),
    ("surface-container-highest", ("neutral", 90), ("neutral", 22)),
    ("on-surface-variant", ("neutralvariant", 30), ("neutralvariant", 80)),
    ("outline", ("neutralvariant", 50), ("neutralvariant", 60)),
    ("outline-variant", ("neutralvariant", 80), ("neutralvariant", 30)),
    ("inverse-surface", ("neutral", 20), ("neutral", 90)),
    ("inverse-on-surface", ("neutral", 95), ("neutral", 20)),
    ("inverse-primary", ("primary", 80), ("primary", 40)),
    ("scrim", ("neutral", 0), ("neutral", 0)),
]


def emit(which, index):
    width = max(len(role) for role, *_ in ROLES)
    print(f"  /* {which} */")
    for row in ROLES:
        role, source = row[0], row[index]
        print(f"  --m3-{role}:{' ' * (width - len(role))} {at(*source)};")


if __name__ == "__main__":
    print(f"/* Generated by scripts/m3-palette.py from seed {SEED}. Do not hand-edit. */")
    print(":root {")
    emit("light", 1)
    print("}\n")
    print("@media (prefers-color-scheme: dark) {")
    print("  :root {")
    emit("dark", 2)
    print("  }")
    print("}")
