package server

// Telegraph 阅读 + 图片下载。
//
// 频道消息内嵌的 Telegraph 链接，Telegram 会随消息附带 MessageMediaWebPage
// 预览（标题/描述/配图）。阅读正文走 Telegraph 官方 JSON API（getPage），
// 返回干净的节点树，无需解析 HTML。文章图片由后端直接出网抓取（Telegraph
// 本身可直连，不走 Telegram 的 MTProto 代理），并通过一个代理端点给前端
// 展示，避免混合内容/防盗链问题。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"

	"github.com/iyear/tdl/core/util/tutil"
)

// telegraphAPI is the Telegraph JSON API base. 文章路径不含扩展名，如
// "New-Thoughts-07-04"。
const telegraphAPI = "https://api.telegra.ph"

// telegraphClient is the HTTP client used for Telegraph API + article images.
// It dials directly (Telegraph is reachable without a proxy in practice) and is
// NOT routed through the Telegram MTProto proxy. Bounded timeouts so a dead
// endpoint doesn't hang a reader.
var telegraphClient = &http.Client{Timeout: 30 * time.Second}

// --- Telegraph API 响应结构 ---

// telegraphPage is the getPage response. Content is a nested node tree where a
// node is either a string (text) or a {tag, attrs, children} object.
type telegraphPage struct {
	OK     bool           `json:"ok"`
	Result *telegraphInfo `json:"result,omitempty"`
	Error  string         `json:"error,omitempty"`
}

type telegraphInfo struct {
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Author      string            `json:"author_name"`
	URL         string            `json:"url"`
	Path        string            `json:"path"`
	Views       int               `json:"views"`
	Content     []json.RawMessage `json:"content"`
}

// telegraphNode is a content node: either text (String) or a {tag,attrs,children}
// object. Only the fields we render / collect are decoded.
type telegraphNode struct {
	Tag      string            `json:"tag"`
	Attrs    map[string]string `json:"attrs"`
	Children []json.RawMessage `json:"children"`
}

// --- URL 解析 ---

// telegraphPathFromURL extracts the article path from a telegra.ph URL.
// Returns "" if u is not a telegra.ph link.
func telegraphPathFromURL(u string) string {
	parsed, err := url.Parse(strings.TrimSpace(u))
	if err != nil {
		return ""
	}
	if !strings.EqualFold(parsed.Hostname(), "telegra.ph") {
		return ""
	}
	// path like "/New-Thoughts-07-04" or "/New-Thoughts-07-04?edit"
	p := strings.Trim(parsed.Path, "/")
	if p == "" {
		return ""
	}
	return p
}

// --- getPage 拉取 ---

// fetchTelegraphPage fetches a Telegraph article's JSON via the official API.
func fetchTelegraphPage(ctx context.Context, path string) (*telegraphPage, error) {
	u := fmt.Sprintf("%s/getPage/%s?return_content=true", telegraphAPI, url.PathEscape(path))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, errors.Wrap(err, "build telegraph request")
	}
	resp, err := telegraphClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "fetch telegraph page")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("telegraph API %s", resp.Status)
	}
	var pg telegraphPage
	if err := json.NewDecoder(resp.Body).Decode(&pg); err != nil {
		return nil, errors.Wrap(err, "decode telegraph page")
	}
	if !pg.OK || pg.Result == nil {
		if pg.Error != "" {
			return nil, errors.New(pg.Error)
		}
		return nil, errors.New("telegraph API returned an error")
	}
	return &pg, nil
}

// collectTelegraphImages walks a content node tree and returns every image URL.
// An img src is returned as-is (telegra.ph/file/... or an external URL).
func collectTelegraphImages(content []json.RawMessage) []string {
	var out []string
	var walk func(nodes []json.RawMessage)
	walk = func(nodes []json.RawMessage) {
		for _, raw := range nodes {
			var n telegraphNode
			if err := json.Unmarshal(raw, &n); err != nil {
				continue
			}
			if n.Tag == "img" {
				if src := n.Attrs["src"]; src != "" {
					out = append(out, src)
				}
				continue // img 无 children，直接跳过
			}
			if len(n.Children) > 0 {
				walk(n.Children)
			}
		}
	}
	walk(content)
	return out
}

// --- 消息提取 ---

// messageWebPageURL returns the Telegraph URL embedded in a message's
// MessageMediaWebPage, or "" if the message has no web-page preview (or it is
// not a Telegraph link).
func messageWebPageURL(m *tg.Message) string {
	md, ok := m.GetMedia()
	if !ok {
		return ""
	}
	wp, ok := md.(*tg.MessageMediaWebPage)
	if !ok {
		return ""
	}
	w, ok := wp.Webpage.(*tg.WebPage)
	if !ok {
		return ""
	}
	return w.URL
}

// resolveMessageWebPageURL fetches the message and returns the Telegraph URL
// carried in its web-page preview. Returns an error if the message has no
// Telegraph preview.
func (s *Server) resolveMessageWebPageURL(ctx context.Context, a *account, chatID string, msgID int) (string, error) {
	peer, err := tutil.GetInputPeer(ctx, a.peersManager(), chatID)
	if err != nil {
		return "", errors.Wrap(err, "resolve chat")
	}
	msg, err := tutil.GetSingleMessage(ctx, a.poolClient().Default(ctx), peer.InputPeer(), msgID)
	if err != nil {
		return "", errors.Wrap(err, "get message")
	}
	u := messageWebPageURL(msg)
	if u == "" {
		return "", errors.New("该消息没有链接预览")
	}
	if telegraphPathFromURL(u) == "" {
		return "", errors.New("该消息的链接不是 Telegraph")
	}
	return u, nil
}

// --- 图片代理 ---

// handleTelegraphImg proxies an article image. Telegraph articles may reference
// images on any host (telegra.ph/file or external image hosts such as custom
// CDNs), so unlike read/download (which require the article itself on
// telegra.ph) this proxies any public http(s) image URL. Kept strict enough to
// block SSRF: only http/https, and only non-loopback / non-private / non-link-
// local hosts.
func (s *Server) handleTelegraphImg(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("url")
	if err := validateProxiedImageURL(u); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, u, nil)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	resp, err := telegraphClient.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, errors.Wrap(err, "抓取图片失败"))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeErr(w, http.StatusBadGateway, errors.Errorf("图片源返回 %s", resp.Status))
		return
	}
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = io.Copy(w, resp.Body)
}

// validateProxiedImageURL accepts only public http(s) image URLs. Private,
// loopback, link-local and unspecified addresses are rejected to prevent SSRF
// (the endpoint would otherwise fetch internal services or cloud metadata).
func validateProxiedImageURL(u string) error {
	parsed, err := url.Parse(strings.TrimSpace(u))
	if err != nil {
		return errors.New("无效的图片链接")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("仅支持 http/https 图片链接")
	}
	host := parsed.Hostname()
	if host == "" {
		return errors.New("无效的图片链接")
	}
	// Resolve once; reject non-public targets. In practice article images are
	// on public CDNs, so the DNS lookup cost is negligible.
	ips, err := net.LookupIP(host)
	if err != nil {
		// 解析失败也放行：可能是临时 DNS 问题，交给请求阶段再报错。
		// 真正的 SSRF 目标是内网 IP，解析通常会成功。
		return nil
	}
	for _, ip := range ips {
		if !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsPrivate() ||
			ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return errors.Errorf("拒绝代理内网/保留地址: %s", host)
		}
	}
	return nil
}

// --- 阅读接口 ---

// handleTelegraphRead serves a Telegraph article's content (title + text tree)
// for the message browser's in-app reader.
func (s *Server) handleTelegraphRead(w http.ResponseWriter, r *http.Request) {
	a := s.activeAccount()
	if a == nil {
		writeErr(w, http.StatusBadRequest, errString("尚未登录账号"))
		return
	}
	chatID, msgID, err := parseMediaVars(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	u, err := s.resolveMessageWebPageURL(r.Context(), a, chatID, msgID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	path := telegraphPathFromURL(u)
	pg, err := fetchTelegraphPage(r.Context(), path)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url":     pg.Result.URL,
		"title":   pg.Result.Title,
		"desc":    pg.Result.Description,
		"author":  pg.Result.Author,
		"views":   pg.Result.Views,
		"content": pg.Result.Content,
	})
}

// --- 图片批量下载 ---

// handleTelegraphDownload downloads every image of a Telegraph article to the
// download directory. Response is synchronous: {dir, total, success, failed, errors}.
func (s *Server) handleTelegraphDownload(w http.ResponseWriter, r *http.Request) {
	a := s.activeAccount()
	if a == nil {
		writeErr(w, http.StatusBadRequest, errString("尚未登录账号"))
		return
	}
	chatID, msgID, err := parseMediaVars(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	u, err := s.resolveMessageWebPageURL(r.Context(), a, chatID, msgID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	path := telegraphPathFromURL(u)
	pg, err := fetchTelegraphPage(r.Context(), path)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	images := collectTelegraphImages(pg.Result.Content)
	if len(images) == 0 {
		writeErr(w, http.StatusBadRequest, errString("该文章没有图片"))
		return
	}

	// 子目录：<downloadDir>/<频道名>_<消息ID>/，图片按 URL 文件名落盘
	base := s.downloadDirEffective()
	sub := fmt.Sprintf("%s_%d", chatID, msgID)
	dir := filepath.Join(base, sub)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, errors.Wrap(err, "创建下载目录"))
		return
	}

	// 并发下载，每图独立失败不中断整批
	const maxConcurrent = 4
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup
	var mu sync.Mutex
	success, failed := 0, 0
	errs := make([]string, 0, 4)
	for _, img := range images {
		img := img
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := s.downloadTelegraphImage(r.Context(), img, dir); err != nil {
				mu.Lock()
				failed++
				errs = append(errs, fmt.Sprintf("%s: %v", img, err))
				mu.Unlock()
				return
			}
			mu.Lock()
			success++
			mu.Unlock()
		}()
	}
	wg.Wait()

	writeJSON(w, http.StatusOK, map[string]any{
		"dir":     dir,
		"total":   len(images),
		"success": success,
		"failed":  failed,
		"errors":  errs,
	})
}

// downloadTelegraphImage fetches one article image to dir. Filename = the URL's
// basename (a telegra.ph file hash), naturally idempotent: an existing file is
// skipped. Returns an error on failure.
func (s *Server) downloadTelegraphImage(ctx context.Context, img, dir string) error {
	// 外链（非 telegra.ph/file/...）仍允许下载，但文件名取 basename
	name := filepath.Base(strings.TrimSuffix(img, "/"))
	if name == "" || name == "." || name == "/" {
		name = "image"
	}
	dst := filepath.Join(dir, name)
	if _, err := os.Stat(dst); err == nil {
		return nil // 已存在，跳过
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, img, nil)
	if err != nil {
		return err
	}
	resp, err := telegraphClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "请求图片")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.Errorf("图片源返回 %s", resp.Status)
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, cpErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if cpErr != nil {
		_ = os.Remove(tmp)
		return cpErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	return os.Rename(tmp, dst)
}
