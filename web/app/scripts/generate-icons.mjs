import { mkdir, readFile, writeFile } from "node:fs/promises"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"
import sharp from "sharp"

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "public", "icons")
const pngSizes = [16, 24, 32, 48, 64, 128, 256, 512]
const faviconSizes = [16, 24, 32, 48]
const appSizes = [16, 24, 32, 48, 64, 128, 256]

await mkdir(root, { recursive: true })
const svg = await readFile(join(root, "icon.svg"))

async function render(size, file = `icon-${size}.png`) {
  const output = await sharp(svg, { density: 384 })
    .resize(size, size, { fit: "fill" })
    .png({ compressionLevel: 9, palette: false })
    .toBuffer()
  await writeFile(join(root, file), output)
  return output
}

function ico(frames) {
  const header = Buffer.alloc(6 + frames.length * 16)
  header.writeUInt16LE(0, 0)
  header.writeUInt16LE(1, 2)
  header.writeUInt16LE(frames.length, 4)
  let imageOffset = header.length
  frames.forEach(({ size, png }, index) => {
    const offset = 6 + index * 16
    header[offset] = size === 256 ? 0 : size
    header[offset + 1] = size === 256 ? 0 : size
    header[offset + 2] = 0
    header[offset + 3] = 0
    header.writeUInt16LE(1, offset + 4)
    header.writeUInt16LE(32, offset + 6)
    header.writeUInt32LE(png.length, offset + 8)
    header.writeUInt32LE(imageOffset, offset + 12)
    imageOffset += png.length
  })
  return Buffer.concat([header, ...frames.map(({ png }) => png)])
}

const pngs = new Map()
for (const size of pngSizes) pngs.set(size, await render(size))
await render(180, "apple-touch-icon.png")
await writeFile(
  join(root, "favicon.ico"),
  ico(faviconSizes.map((size) => ({ size, png: pngs.get(size) }))),
)
await writeFile(
  join(root, "app.ico"),
  ico(appSizes.map((size) => ({ size, png: pngs.get(size) }))),
)
console.log("icon assets generated")
