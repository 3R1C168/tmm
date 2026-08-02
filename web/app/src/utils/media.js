// 媒体类型判断（跨组件复用，避免正则重复抄写）

export function isImage(media) {
  return media && /\.(jpe?g|png|gif|webp|bmp)$/i.test(media.name)
}

export function isVideo(media) {
  return media && /\.(mp4|mov|mkv|webm|avi)$/i.test(media.name)
}

export function isAudio(media) {
  return media && (/\.(mp3|m4a|aac|ogg|opus|flac|wav)$/i.test(media.name) || /^audio\//i.test(media.mime || ''))
}
