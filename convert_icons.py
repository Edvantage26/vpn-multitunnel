#!/usr/bin/env python3
"""Convert icon_raw.jpg to all required icon formats for VPN MultiTunnel"""

from PIL import Image, ImageDraw
import numpy as np
import os


def remove_dark_background(source_image, luminance_threshold=80):
    """Remove the dark background from an image by making dark pixels transparent.

    Pixels with luminance below the threshold get alpha=0 (fully transparent).
    A smooth gradient is applied near the threshold for anti-aliasing.
    """
    img_array = np.array(source_image.convert('RGBA'), dtype=np.float64)

    # Calculate luminance from RGB channels
    pixel_luminance = (
        0.299 * img_array[:, :, 0]
        + 0.587 * img_array[:, :, 1]
        + 0.114 * img_array[:, :, 2]
    )

    # Create smooth alpha transition around the threshold
    fade_range = 30  # pixels within this range get partial transparency
    lower_bound = luminance_threshold - fade_range / 2
    upper_bound = luminance_threshold + fade_range / 2

    alpha_mask = np.clip(
        (pixel_luminance - lower_bound) / (upper_bound - lower_bound),
        0.0,
        1.0,
    )

    img_array[:, :, 3] = alpha_mask * 255
    return Image.fromarray(img_array.astype(np.uint8), 'RGBA')


def add_green_dot(source_image):
    """Add a green status dot to the bottom-right corner of the image."""
    result = source_image.copy()
    draw = ImageDraw.Draw(result)
    icon_size = result.size[0]

    # Dot size relative to icon
    dot_radius = max(icon_size // 5, 2)
    dot_center_x = icon_size - dot_radius - 1
    dot_center_y = icon_size - dot_radius - 1

    # Draw filled green circle
    draw.ellipse(
        [
            dot_center_x - dot_radius,
            dot_center_y - dot_radius,
            dot_center_x + dot_radius,
            dot_center_y + dot_radius,
        ],
        fill=(0, 200, 60, 255),
    )
    return result


def main():
    # Paths
    base_dir = os.path.dirname(os.path.abspath(__file__))
    source = os.path.join(base_dir, "build", "appicon.png")

    # Output paths
    windows_ico = os.path.join(base_dir, "build", "windows", "icon.ico")
    tray_ico = os.path.join(base_dir, "internal", "tray", "icon.ico")
    tray_connected_ico = os.path.join(base_dir, "internal", "tray", "icon_connected.ico")
    appicon_png = os.path.join(base_dir, "build", "appicon.png")
    logo_png = os.path.join(base_dir, "frontend", "src", "assets", "images", "logo-universal.png")

    # Load source image
    print(f"Loading source: {source}")
    img = Image.open(source)

    # Convert to RGBA (for transparency support in PNG)
    if img.mode != 'RGBA':
        img = img.convert('RGBA')

    # Ensure it's square by cropping to center
    width, height = img.size
    if width != height:
        size = min(width, height)
        left = (width - size) // 2
        top = (height - size) // 2
        img = img.crop((left, top, left + size, top + size))
        print(f"Cropped to square: {size}x{size}")

    # 1. Build appicon.png (512x512)
    print(f"Creating: {appicon_png}")
    appicon = img.resize((512, 512), Image.Resampling.LANCZOS)
    appicon.save(appicon_png, "PNG")

    # 2. Build logo-universal.png (256x256)
    os.makedirs(os.path.dirname(logo_png), exist_ok=True)
    print(f"Creating: {logo_png}")
    logo = img.resize((256, 256), Image.Resampling.LANCZOS)
    logo.save(logo_png, "PNG")

    # 3. Build windows icon.ico (multi-resolution: 16, 32, 48, 256)
    print(f"Creating: {windows_ico}")
    sizes = [(16, 16), (32, 32), (48, 48), (256, 256)]
    icons = [img.resize(size, Image.Resampling.LANCZOS) for size in sizes]
    icons[-1].save(windows_ico, format='ICO', append_images=icons[:-1], sizes=sizes)

    # 4. Build tray icons with transparent background
    print(f"Creating: {tray_ico} (transparent background)")
    tray_source = remove_dark_background(img, luminance_threshold=80)
    tray_sizes = [(16, 16), (32, 32)]
    tray_icons = [tray_source.resize(size, Image.Resampling.LANCZOS) for size in tray_sizes]
    tray_icons[-1].save(tray_ico, format='ICO', append_images=tray_icons[:-1], sizes=tray_sizes)

    # 5. Build tray connected icon (same + green dot)
    print(f"Creating: {tray_connected_ico} (with green dot)")
    tray_connected_icons = [add_green_dot(icon) for icon in tray_icons]
    tray_connected_icons[-1].save(
        tray_connected_ico,
        format='ICO',
        append_images=tray_connected_icons[:-1],
        sizes=tray_sizes,
    )

    print("\nDone! All icons created:")
    print(f"  - {windows_ico}")
    print(f"  - {tray_ico} (transparent background)")
    print(f"  - {tray_connected_ico} (with green dot)")
    print(f"  - {appicon_png}")
    print(f"  - {logo_png}")


if __name__ == "__main__":
    main()
