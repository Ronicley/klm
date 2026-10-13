export async function prepareProjectIcon(file: File): Promise<string> {
  if (!['image/png', 'image/jpeg', 'image/webp'].includes(file.type) || file.size > 5 * 1024 * 1024) {
    throw new Error('Choose a PNG, JPEG, or WebP image smaller than 5 MB.');
  }
  const url = URL.createObjectURL(file);
  try {
    const image = new Image(); image.src = url; await image.decode();
    const canvas = document.createElement('canvas'); canvas.width = 96; canvas.height = 96;
    const context = canvas.getContext('2d');
    if (!context) throw new Error('Image processing unavailable');
    const size = Math.min(image.naturalWidth, image.naturalHeight);
    context.drawImage(image, (image.naturalWidth - size) / 2, (image.naturalHeight - size) / 2, size, size, 0, 0, 96, 96);
    return canvas.toDataURL('image/png');
  } catch {
    throw new Error('This image could not be opened. Please choose another image.');
  } finally { URL.revokeObjectURL(url); }
}
