import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "public", "icons")
const pngSizes = [16, 24, 32, 48, 64, 128, 256, 512]

function pngSize(buffer) {
  assert.deepEqual([...buffer.subarray(1, 4)], [0x50, 0x4e, 0x47], "not a PNG")
  return [buffer.readUInt32BE(16), buffer.readUInt32BE(20)]
}

function icoSizes(buffer) {
  assert.equal(buffer.readUInt16LE(0), 0)
  assert.equal(buffer.readUInt16LE(2), 1)
  const count = buffer.readUInt16LE(4)
  return Array.from({ length: count }, (_, index) => {
    const offset = 6 + index * 16
    return [buffer[offset] || 256, buffer[offset + 1] || 256]
  })
}

for (const size of pngSizes) {
  const buffer = await readFile(join(root, `icon-${size}.png`))
  assert.deepEqual(pngSize(buffer), [size, size])
}

assert.deepEqual(
  pngSize(await readFile(join(root, "apple-touch-icon.png"))),
  [180, 180],
)
assert.deepEqual(
  icoSizes(await readFile(join(root, "favicon.ico"))),
  [16, 24, 32, 48].map((size) => [size, size]),
)
assert.deepEqual(
  icoSizes(await readFile(join(root, "app.ico"))),
  [16, 24, 32, 48, 64, 128, 256].map((size) => [size, size]),
)

const svg = await readFile(join(root, "icon.svg"), "utf8")
for (const value of ["#9C5CF0", "#5133A6", "#C8A7F6", "#F4EEFF", "#6841BE"]) {
  assert.match(svg, new RegExp(value, "i"))
}
assert.match(svg, /viewBox="0 0 256 256"/)
console.log("icon assets verified")
