// Album (grouped media) mosaic layout, ported from telegram-tt
// (src/components/middle/message/helpers/calculateAlbumLayout.ts), itself a
// port of Telegram Desktop / TelegramSwift grouping. Instead of cropping every
// thumb to a fixed square, it packs items by aspect ratio into rows so each
// keeps its own shape. Returns per-item {x,y,width,height} for absolute
// positioning inside a container of {width,height}.

const MAX_ROW_ITEMS = 3
const MAX_LAST_ROW_ITEMS = 4
const MIN_EXTENDED_ROWS = 5
const EXTENDED_EXTRA_ROWS = 2

// proportions: ratio>1.2 → 'w' (wide), <0.8 → 'n' (narrow), else 'q' (square)
function toProportions(ratios) {
  return ratios.map((r) => (r > 1.2 ? 'w' : r < 0.8 ? 'n' : 'q')).join('')
}

function clamp(v, lo, hi) { return Math.max(lo, Math.min(hi, v)) }

// ratios: array of width/height for each item; maxWidth: container width;
// spacing: gap px. Returns { items: [{x,y,width,height}], width, height }.
export function calculateAlbumLayout(ratios, maxWidth, spacing = 2, maxHeight = maxWidth) {
  const count = ratios.length
  if (count === 0) return { items: [], width: 0, height: 0 }
  const proportions = toProportions(ratios)
  const averageRatio = (ratios.reduce((a, b) => a + b, 0) + 1) / count

  if (count === 1) return layoutSingle(ratios, maxWidth, spacing, maxHeight)
  if (count >= MIN_EXTENDED_ROWS || ratios.some((r) => r > 2)) {
    return layoutComplex(ratios, proportions, averageRatio, maxWidth, spacing)
  }
  if (count === 2) return layoutTwo(ratios, proportions, averageRatio, maxWidth, spacing, maxHeight)
  if (count === 3) return layoutThree(ratios, proportions, maxWidth, spacing, maxHeight)
  return layoutFour(ratios, proportions, maxWidth, spacing, maxHeight)
}

function finish(items, spacing) {
  let width = 0; let height = 0
  for (const it of items) {
    width = Math.max(width, it.x + it.width)
    height = Math.max(height, it.y + it.height)
  }
  return { items, width, height }
}

function layoutSingle(ratios, maxWidth, spacing, maxHeight) {
  const r = ratios[0]
  const height = Math.round(Math.min(maxWidth / r, maxHeight))
  return finish([{ x: 0, y: 0, width: maxWidth, height }], spacing)
}

function layoutTwo(ratios, props, avgRatio, maxWidth, spacing, maxHeight) {
  const [r0, r1] = ratios
  const minWidth = 100
  if (props === 'ww' && avgRatio > 1.4 && Math.abs(r0 - r1) < 0.2) {
    // stacked (both very wide)
    const height = Math.round(Math.min(maxWidth / r0, maxWidth / r1, (maxHeight - spacing) / 2))
    return finish([
      { x: 0, y: 0, width: maxWidth, height },
      { x: 0, y: height + spacing, width: maxWidth, height },
    ], spacing)
  }
  if (props === 'ww' || props === 'qq') {
    // side by side, equal width
    const width = (maxWidth - spacing) / 2
    const height = Math.round(Math.min(width / r0, width / r1, maxHeight))
    return finish([
      { x: 0, y: 0, width, height },
      { x: width + spacing, y: 0, width, height },
    ], spacing)
  }
  // side by side, unequal width
  const minimalWidth = Math.round(1.5 * minWidth)
  let w1 = Math.max(0.4 * (maxWidth - spacing), (maxWidth - spacing) / r0 / (1 / r0 + 1 / r1))
  w1 = Math.min(w1, maxWidth - spacing - minimalWidth)
  w1 = Math.round(w1)
  const w0 = maxWidth - w1 - spacing
  const height = Math.round(Math.min(maxHeight, Math.min(w0 / r0, w1 / r1)))
  return finish([
    { x: 0, y: 0, width: w0, height },
    { x: w0 + spacing, y: 0, width: w1, height },
  ], spacing)
}

function layoutThree(ratios, props, maxWidth, spacing, maxHeight) {
  const [r0, r1, r2] = ratios
  const minWidth = 100
  if (props[0] === 'n') {
    // big left, two stacked right
    const h0 = maxHeight
    const h2 = Math.round(Math.min((maxHeight - spacing) / 2, (r1 * (maxWidth - spacing)) / (r2 + r1)))
    const h1 = maxHeight - h2 - spacing
    let rightWidth = Math.max(minWidth, Math.min(maxWidth / 2, Math.round(Math.min(h1 * r1, h2 * r2))))
    const leftWidth = Math.min(Math.round(h0 * r0), maxWidth - spacing - rightWidth)
    return finish([
      { x: 0, y: 0, width: leftWidth, height: h0 },
      { x: leftWidth + spacing, y: 0, width: rightWidth, height: h1 },
      { x: leftWidth + spacing, y: h1 + spacing, width: rightWidth, height: h2 },
    ], spacing)
  }
  // big top, two side by side below
  const h0 = Math.round(Math.min(maxWidth / r0, 0.66 * (maxHeight - spacing)))
  const w = (maxWidth - spacing) / 2
  const h = Math.min(maxHeight - h0 - spacing, Math.round(Math.min(w / r1, w / r2)))
  return finish([
    { x: 0, y: 0, width: maxWidth, height: h0 },
    { x: 0, y: h0 + spacing, width: w, height: h },
    { x: w + spacing, y: h0 + spacing, width: w, height: h },
  ], spacing)
}

function layoutFour(ratios, props, maxWidth, spacing, maxHeight) {
  const [r0, r1, r2, r3] = ratios
  const minWidth = 100
  if (props[0] === 'w') {
    // big top, three below
    const h0 = Math.round(Math.min(maxWidth / r0, 0.66 * (maxHeight - spacing)))
    const h = Math.round((maxWidth - 2 * spacing) / (r1 + r2 + r3))
    const w0 = Math.max(minWidth, Math.round(Math.min(0.4 * (maxWidth - 2 * spacing), h * r1)))
    const w2 = Math.round(Math.max(Math.max(minWidth, 0.33 * (maxWidth - 2 * spacing)), h * r3))
    const w1 = maxWidth - w0 - w2 - 2 * spacing
    return finish([
      { x: 0, y: 0, width: maxWidth, height: h0 },
      { x: 0, y: h0 + spacing, width: w0, height: h },
      { x: w0 + spacing, y: h0 + spacing, width: w1, height: h },
      { x: w0 + w1 + 2 * spacing, y: h0 + spacing, width: w2, height: h },
    ], spacing)
  }
  // big left, three stacked right
  const w0 = Math.round(Math.min(maxHeight * r0, 0.6 * (maxWidth - spacing)))
  const w = Math.round((maxHeight - 2 * spacing) / (1 / r1 + 1 / r2 + 1 / r3))
  const h1 = Math.round(w / r1)
  const h2 = Math.round(w / r2)
  const h3 = maxHeight - h1 - h2 - 2 * spacing
  return finish([
    { x: 0, y: 0, width: w0, height: maxHeight },
    { x: w0 + spacing, y: 0, width: w, height: h1 },
    { x: w0 + spacing, y: h1 + spacing, width: w, height: h2 },
    { x: w0 + spacing, y: h1 + h2 + 2 * spacing, width: w, height: h3 },
  ], spacing)
}

// complex layout: enumerate row splits, pick the one whose total height is
// closest to the target (4/3 of maxWidth), penalizing tiny rows and
// top-heavier-than-bottom arrangements.
function layoutComplex(ratios, props, avgRatio, maxWidth, spacing) {
  const minWidth = 100
  const targetHeight = (4 * maxWidth) / 3
  // clamp crop ratios like the official client
  const cropped = ratios.map((r) => (avgRatio > 1.1 ? clamp(r, 1, 2.75) : clamp(r, 0.6667, 1)))
  const count = ratios.length

  // generate candidate row splits (arrays of row sizes)
  const splits = []
  const gen = (rows, maxPerRow, lastMax, acc) => {
    if (acc.reduce((a, b) => a + b, 0) === count) { splits.push([...acc]); return }
    if (acc.length >= rows) return
    for (let n = 1; n <= (acc.length === rows - 1 ? lastMax : maxPerRow); n++) {
      if (acc.reduce((a, b) => a + b, 0) + n <= count) { acc.push(n); gen(rows, maxPerRow, lastMax, acc); acc.pop() }
    }
  }
  for (let rows = 2; rows <= 4; rows++) gen(rows, MAX_ROW_ITEMS, MAX_LAST_ROW_ITEMS, [])
  // extended fallback
  if (!splits.length) {
    for (let rows = MIN_EXTENDED_ROWS; rows <= MIN_EXTENDED_ROWS + EXTENDED_EXTRA_ROWS; rows++) {
      gen(rows, MAX_ROW_ITEMS, MAX_LAST_ROW_ITEMS, [])
    }
  }

  let best = null
  for (const split of splits) {
    const lineHeights = []
    let idx = 0
    let ok = true
    for (const n of split) {
      const rowRatios = cropped.slice(idx, idx + n)
      const sum = rowRatios.reduce((a, b) => a + b, 0)
      if (sum <= 0) { ok = false; break }
      lineHeights.push((maxWidth - (n - 1) * spacing) / sum)
      idx += n
    }
    if (!ok) continue
    const totalHeight = lineHeights.reduce((a, b) => a + b, 0) + spacing * (split.length - 1)
    const minLine = Math.min(...lineHeights)
    let penalty = 1
    if (minLine < minWidth) penalty *= 1.5
    for (let i = 0; i + 1 < split.length; i++) if (split[i] > split[i + 1]) penalty *= 1.5
    const score = Math.abs(totalHeight - targetHeight) * penalty
    if (!best || score < best.score) best = { split, lineHeights, score }
  }
  if (!best) { // last resort: one column
    const items = []; let y = 0
    for (const r of ratios) { const h = Math.round(maxWidth / r); items.push({ x: 0, y, width: maxWidth, height: h }); y += h + spacing }
    return finish(items, spacing)
  }

  const items = []
  let y = 0; let idx = 0
  best.split.forEach((n, rowIdx) => {
    const lineHeight = best.lineHeights[rowIdx]
    let x = 0
    for (let k = 0; k < n; k++) {
      const r = cropped[idx]
      const width = k === n - 1 ? maxWidth - x : Math.round(r * lineHeight)
      items.push({ x, y, width, height: Math.round(lineHeight) })
      x += width + spacing
      idx++
    }
    y += Math.round(lineHeight) + spacing
  })
  return finish(items, spacing)
}
