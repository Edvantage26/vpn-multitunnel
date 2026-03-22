#!/usr/bin/env python3
"""Convert icon source to all required icon formats for VPN MultiTunnel.

Generates BMP-based ICO files (not PNG-in-ICO) for maximum Windows compatibility
with LoadImage API used by energye/systray.
"""

from PIL import Image, ImageDraw
import numpy as np
import struct
import os


def remove_dark_background(source_image, luminance_threshold=80):
    """Remove the dark background by making dark pixels transparent."""
    img_array = np.array(source_image.convert('RGBA'), dtype=np.float64)
    pixel_luminance = (
        0.299 * img_array[:, :, 0]
        + 0.587 * img_array[:, :, 1]
        + 0.114 * img_array[:, :, 2]
    )
    fade_range = 30
    lower_bound = luminance_threshold - fade_range / 2
    upper_bound = luminance_threshold + fade_range / 2
    alpha_mask = np.clip(
        (pixel_luminance - lower_bound) / (upper_bound - lower_bound),
        0.0, 1.0,
    )
    img_array[:, :, 3] = alpha_mask * 255
    return Image.fromarray(img_array.astype(np.uint8), 'RGBA')


def add_green_dot(source_image):
    """Add a green status dot to the bottom-right corner."""
    result = source_image.copy()
    draw = ImageDraw.Draw(result)
    icon_size = result.size[0]
    dot_radius = max(icon_size // 5, 2)
    dot_center_x = icon_size - dot_radius - 1
    dot_center_y = icon_size - dot_radius - 1
    draw.ellipse(
        [
            dot_center_x - dot_radius, dot_center_y - dot_radius,
            dot_center_x + dot_radius, dot_center_y + dot_radius,
        ],
        fill=(0, 200, 60, 255),
    )
    return result


def image_to_bmp_ico(images):
    """Build an ICO file with BMP (not PNG) entries for Windows LoadImage compatibility.

    Args:
        images: list of PIL Image objects (RGBA mode), one per size entry.

    Returns:
        bytes of the complete ICO file.
    """
    num_images = len(images)
    ico_header = struct.pack('<HHH', 0, 1, num_images)  # reserved, type=ICO, count

    directory_entries = []
    bmp_data_blocks = []

    # Calculate where image data starts (after header + all directory entries)
    data_start_offset = 6 + num_images * 16

    current_data_offset = data_start_offset
    for img in images:
        img_rgba = img.convert('RGBA')
        img_width = img_rgba.width
        img_height = img_rgba.height

        # Build BITMAPINFOHEADER (40 bytes) + pixel data + AND mask
        dib_header_size = 40
        pixel_data_size = img_width * img_height * 4
        and_mask_row_bytes = ((img_width + 31) // 32) * 4
        and_mask_size = and_mask_row_bytes * img_height
        total_bmp_size = dib_header_size + pixel_data_size + and_mask_size

        bmp_buffer = bytearray(total_bmp_size)

        # BITMAPINFOHEADER
        struct.pack_into('<I', bmp_buffer, 0, dib_header_size)           # biSize
        struct.pack_into('<i', bmp_buffer, 4, img_width)                 # biWidth
        struct.pack_into('<i', bmp_buffer, 8, img_height * 2)            # biHeight (doubled for ICO)
        struct.pack_into('<H', bmp_buffer, 12, 1)                        # biPlanes
        struct.pack_into('<H', bmp_buffer, 14, 32)                       # biBitCount
        struct.pack_into('<I', bmp_buffer, 16, 0)                        # biCompression (BI_RGB)
        struct.pack_into('<I', bmp_buffer, 20, pixel_data_size)          # biSizeImage

        # Write BGRA pixel data, bottom-up
        pixels = img_rgba.load()
        pixel_offset = dib_header_size
        for row_y in range(img_height - 1, -1, -1):  # bottom-up
            for col_x in range(img_width):
                red, green, blue, alpha = pixels[col_x, row_y]
                struct.pack_into('BBBB', bmp_buffer, pixel_offset, blue, green, red, alpha)
                pixel_offset += 4

        # AND mask: all zeros (alpha channel handles transparency)
        # Already zeroed by bytearray()

        # Directory entry
        ico_dir_width = 0 if img_width == 256 else img_width
        ico_dir_height = 0 if img_height == 256 else img_height
        dir_entry = struct.pack(
            '<BBBBHHII',
            ico_dir_width,       # width
            ico_dir_height,      # height
            0,                   # color palette
            0,                   # reserved
            1,                   # color planes
            32,                  # bits per pixel
            total_bmp_size,      # image data size
            current_data_offset, # image data offset
        )

        directory_entries.append(dir_entry)
        bmp_data_blocks.append(bytes(bmp_buffer))
        current_data_offset += total_bmp_size

    return ico_header + b''.join(directory_entries) + b''.join(bmp_data_blocks)


def main():
    base_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    source = os.path.join(base_dir, "build", "appicon.png")

    # Output paths
    windows_ico = os.path.join(base_dir, "build", "windows", "icon.ico")
    tray_ico = os.path.join(base_dir, "internal", "tray", "icon.ico")
    tray_connected_ico = os.path.join(base_dir, "internal", "tray", "icon_connected.ico")
    appicon_png = os.path.join(base_dir, "build", "appicon.png")
    logo_png = os.path.join(base_dir, "frontend", "src", "assets", "images", "logo-universal.png")

    print(f"Loading source: {source}")
    img = Image.open(source)

    if img.mode != 'RGBA':
        img = img.convert('RGBA')

    # Ensure square
    width, height = img.size
    if width != height:
        size = min(width, height)
        left = (width - size) // 2
        top = (height - size) // 2
        img = img.crop((left, top, left + size, top + size))
        print(f"Cropped to square: {size}x{size}")

    # 1. logo-universal.png (256x256)
    os.makedirs(os.path.dirname(logo_png), exist_ok=True)
    print(f"Creating: {logo_png}")
    logo = img.resize((256, 256), Image.Resampling.LANCZOS)
    logo.save(logo_png, "PNG")

    # 2. Windows icon.ico (multi-resolution, BMP format)
    print(f"Creating: {windows_ico} (BMP format)")
    win_sizes = [16, 32, 48, 256]
    win_images = [img.resize((sz, sz), Image.Resampling.LANCZOS) for sz in win_sizes]
    with open(windows_ico, 'wb') as out_file:
        out_file.write(image_to_bmp_ico(win_images))

    # 3. Tray icon (transparent background, BMP format)
    print(f"Creating: {tray_ico} (transparent, BMP format)")
    tray_source = remove_dark_background(img, luminance_threshold=80)
    tray_sizes = [16, 32]
    tray_images = [tray_source.resize((sz, sz), Image.Resampling.LANCZOS) for sz in tray_sizes]
    with open(tray_ico, 'wb') as out_file:
        out_file.write(image_to_bmp_ico(tray_images))

    # 4. Tray connected icon (+ green dot, BMP format)
    print(f"Creating: {tray_connected_ico} (with green dot, BMP format)")
    tray_connected_images = [add_green_dot(icon) for icon in tray_images]
    with open(tray_connected_ico, 'wb') as out_file:
        out_file.write(image_to_bmp_ico(tray_connected_images))

    print("\nDone! All icons created (BMP format for Windows compatibility):")
    for path in [windows_ico, tray_ico, tray_connected_ico, appicon_png, logo_png]:
        size = os.path.getsize(path)
        print(f"  - {path} ({size} bytes)")


if __name__ == "__main__":
    main()
