package admin

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tsawler/cms/media"
	"github.com/tsawler/cms/render"
)

// uploadLimit is the request-body cap for media uploads: big enough for
// the largest allowed video, and never below the image/document limit.
func (s *server) uploadLimit() int64 {
	limit := int64(media.MaxImageDocBytes)
	if v := s.deps.Media.MaxVideoBytes(); v > limit {
		limit = v
	}
	return limit
}

func (s *server) uploadTooLargeMsg(r *http.Request) string {
	return fmt.Sprintf(s.tr(r, "That file is too large — images and documents must be under %d MB, videos under %d MB."),
		media.MaxImageDocBytes>>20, s.deps.Media.MaxVideoBytes()>>20)
}

// tooManyPixelsMsg is uploadTooLargeMsg for an image whose bytes are
// within the cap but whose dimensions are not: the fix is resizing it,
// which the byte-size message would never suggest.
func (s *server) tooManyPixelsMsg(r *http.Request) string {
	return fmt.Sprintf(s.tr(r, "That image's dimensions are too large to process — it must be under %d megapixels. Resize it and try again."),
		media.MaxImagePixels/1_000_000)
}

const unsupportedTypeMsg = "That file type isn't supported. Use an image (JPEG, PNG, GIF, WebP, SVG), video (MP4, WebM), PDF, office document, text/CSV, or ZIP."

const unsafeSVGMsg = "That SVG can't be used — it contains scripts or other active content."

// folderNotEmptyMsg explains a refused folder delete. The media template
// puts the same words on the disabled button, so the reason reads the same
// whether you hover it or manage to submit anyway.
const folderNotEmptyMsg = "Move or delete its files first — this folder isn't empty."

func (s *server) mediaList(w http.ResponseWriter, r *http.Request) {
	s.renderMediaList(w, r, http.StatusOK, "")
}

// parseFolderParam maps a folder query/form value to list options: "" means
// no filter, "root" means unfiled, digits mean a folder id.
func parseFolderParam(v string) (folderID *int64, unfiled bool) {
	if v == "root" {
		return nil, true
	}
	if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
		return &id, false
	}
	return nil, false
}

// mediaTab validates a tab query/form value; anything unknown lands on
// the images tab.
func mediaTab(v string) string {
	switch v {
	case "documents", "videos":
		return v
	}
	return "images"
}

// kindForTab maps a media tab to the kind whose items and folders it
// lists.
func kindForTab(tab string) media.Kind {
	switch tab {
	case "documents":
		return media.KindFile
	case "videos":
		return media.KindVideo
	}
	return media.KindImage
}

// formImageChoices bounds the image <select> on the page and post forms:
// the newest this many images, rather than the whole library rendered
// into one control. The in-place editor's picker pages through them all.
var formImageChoices = 200

// imageChoices returns the newest images for a form's <select>, plus
// whether there are more than it holds. keep names a library image the
// form already has chosen, which is listed whatever its age: a choice
// missing from the options would be dropped by the browser, and saving
// the form would then clear it.
func (s *server) imageChoices(ctx context.Context, keep int64) ([]media.View, bool, error) {
	items, err := s.deps.Media.All(ctx, s.deps.DefaultLocale,
		media.ListOptions{Kind: media.KindImage, Limit: formImageChoices + 1})
	if err != nil {
		return nil, false, err
	}
	more := len(items) > formImageChoices
	items = items[:min(len(items), formImageChoices)]
	if keep != 0 && !slices.ContainsFunc(items, func(md media.Media) bool { return md.ID == keep }) {
		if kept, err := s.deps.Media.ByIDs(ctx, []int64{keep}, s.deps.DefaultLocale); err == nil {
			items = append(items, kept...)
		} else {
			return nil, false, err
		}
	}
	return s.deps.Media.Views(items), more, nil
}

// mediaPerPage is how many items one page of the media library shows: a
// grid of thumbnails takes more at a glance than a table of rows does.
// A variable so tests can page through a handful.
var mediaPerPage = 100

// mediaCounts are the media page's tab badges, one per kind.
type mediaCounts struct {
	Images, Documents, Videos int
}

// mediaPageURL builds the media library's page links. Always on /media,
// whatever handled the request — the page is also drawn as the answer to
// a refused upload, whose own path is no place to page through — with
// the tab, search, folder and order kept.
func (s *server) mediaPageURL(tab, query, folder string, order mediaOrder) func(int) string {
	return func(n int) string {
		q := url.Values{}
		q.Set("tab", tab)
		if query != "" {
			q.Set("q", query)
		}
		if folder != "" {
			q.Set("folder", folder)
		}
		q.Set("sort", order.Key)
		q.Set("dir", order.dir())
		if n > 1 {
			q.Set("page", strconv.Itoa(n))
		}
		return s.deps.AdminPath + "/media?" + q.Encode()
	}
}

// mediaOrder is how the media page orders its items: a column and a
// direction.
type mediaOrder struct {
	Key  string // name, kind, size, dims, or added
	Desc bool
}

func (o mediaOrder) dir() string {
	if o.Desc {
		return "desc"
	}
	return "asc"
}

// mediaSortCookie remembers the order chosen on the media page, which the
// page's script sets when a column is clicked. It lets a plain link to
// the library — the sidebar, a folder, a tab — come back in that order.
const mediaSortCookie = "cms_admin_media_sort"

// mediaOrderFor is the order a media page request asks for: ?sort= and
// ?dir= when the URL carries them, the remembered choice otherwise, and
// by name, A to Z, when neither says anything usable.
//
// The server orders the library because a page holds only mediaPerPage
// items: sorted in the browser, "by name" meant by name within whichever
// hundred items the page happened to hold.
func mediaOrderFor(r *http.Request) mediaOrder {
	key, dir := r.URL.Query().Get("sort"), r.URL.Query().Get("dir")
	if key == "" {
		if c, err := r.Cookie(mediaSortCookie); err == nil {
			key, dir, _ = strings.Cut(c.Value, ":")
		}
	}
	if !media.ValidSort(key) {
		return mediaOrder{Key: "name"}
	}
	return mediaOrder{Key: key, Desc: dir == "desc"}
}

func (s *server) renderMediaList(w http.ResponseWriter, r *http.Request, status int, formError string) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	folderParam := r.URL.Query().Get("folder")
	folderID, _ := parseFolderParam(folderParam)
	tab := mediaTab(r.URL.Query().Get("tab"))

	allFolders, err := s.deps.Media.Folders(r.Context())
	if err != nil {
		s.serverError(w, err)
		return
	}

	data := s.newTemplateData(r)
	kind := kindForTab(tab)

	// Directory-style browsing: without a search the page shows one
	// directory — the root (unfiled items plus the folder entries) or
	// one folder's contents. A search spans every folder and lists
	// results flat.
	opts := media.ListOptions{Query: query, Kind: kind}
	if query == "" {
		if folderID != nil {
			// Only this tab's kind resolves: a folder link carried over
			// from another tab (or a stale URL) falls back to this tab's
			// root rather than showing a folder the tab doesn't have.
			for i := range allFolders {
				if allFolders[i].ID == *folderID && allFolders[i].Kind == kind {
					data.CurrentFolder = &allFolders[i]
					break
				}
			}
		}
		if data.CurrentFolder != nil {
			opts.FolderID = folderID
		} else {
			opts.Unfiled = true // root; stale folder links land here too
			folderParam = ""
		}
	}

	// The tab's own kind, a page at a time; the badges on the other tabs
	// come from one grouped count. The page used to load every item of
	// every kind and keep one kind's worth.
	byKind, err := s.deps.Media.CountByKind(r.Context(), opts)
	if err != nil {
		s.serverError(w, err)
		return
	}
	order := mediaOrderFor(r)
	opts.Sort, opts.Desc = order.Key, order.Desc
	pager := render.NewPager(listPage(r), mediaPerPage, byKind[kind], s.mediaPageURL(tab, query, folderParam, order))
	pager.PrevLabel, pager.NextLabel = s.tr(r, "Previous"), s.tr(r, "Next")
	opts.Limit, opts.Offset = pager.PerPage, pager.Offset()
	items, err := s.deps.Media.All(r.Context(), s.deps.DefaultLocale, opts)
	if err != nil {
		s.serverError(w, err)
		return
	}
	data.MediaCounts = mediaCounts{
		Images:    byKind[media.KindImage],
		Documents: byKind[media.KindFile],
		Videos:    byKind[media.KindVideo],
	}
	data.Pager = pager

	// Each tab lists only its own kind's folders, and new folders are
	// created in the kind being browsed.
	folders := make([]media.Folder, 0, len(allFolders))
	for _, f := range allFolders {
		if f.Kind == kind {
			folders = append(folders, f)
		}
	}
	// Folders come by name whatever the column — they have no size and no
	// dimensions to sort by — but follow the chosen direction.
	if order.Desc {
		slices.Reverse(folders)
	}
	data.MediaOrder = order
	for _, v := range s.deps.Media.Views(items) {
		switch v.Kind {
		case media.KindFile:
			data.Documents = append(data.Documents, v)
		case media.KindVideo:
			data.Videos = append(data.Videos, v)
		default:
			data.Media = append(data.Media, v)
		}
	}
	switch kind {
	case media.KindFile:
		data.Entries = data.Documents
	case media.KindVideo:
		data.Entries = data.Videos
	default:
		data.Entries = data.Media
	}
	data.Folders = folders
	data.PageScript = "media.js"
	data.PageWide = true
	data.MediaQuery = query
	data.MediaFolder = folderParam
	data.MediaTab = tab
	data.MediaKind = string(kind)
	data.MaxVideoMB = s.deps.Media.MaxVideoBytes() >> 20
	data.Error = formError
	s.render(w, status, "media", data)
}

// fileInto files a freshly uploaded item into the folder it was aimed at,
// undoing the filing when the folder holds a different kind. Folders belong
// to exactly one kind, so a PDF dropped into an image folder would be
// stored somewhere the Documents tab can never list it — dropping it back
// to unfiled at least leaves it findable. Reports whether it stayed put.
//
// Uploading several files at once is what makes this reachable: one
// destination, and a batch that need not be all of one kind.
func (s *server) fileInto(ctx context.Context, md *media.Media, folderID *int64) (filed bool, err error) {
	if folderID == nil {
		return true, nil
	}
	folders, err := s.deps.Media.Folders(ctx)
	if err != nil {
		return false, err
	}
	for _, f := range folders {
		if f.ID == *folderID {
			if f.Kind == md.Kind {
				return true, nil
			}
			break
		}
	}
	if err := s.deps.Media.Move(ctx, md.ID, nil); err != nil {
		return false, err
	}
	md.FolderID = nil
	return false, nil
}

// backToMedia redirects to the referring media view, so the active tab
// and filters survive the round-trip; without a Referer (direct POST,
// privacy proxy) it falls back to the media page's default view.
func (s *server) backToMedia(w http.ResponseWriter, r *http.Request) {
	dest := r.Header.Get("Referer")
	if dest == "" {
		dest = s.deps.AdminPath + "/media"
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

func (s *server) mediaUpload(w http.ResponseWriter, r *http.Request) {
	// No ceiling set here: the CSRF middleware bounded the body before
	// anything read it, which is the only place a limit can still take
	// effect (see readToken). What is over the *upload* limit rather
	// than the request limit is caught by the manager, from the
	// multipart header's declared size, on either path.
	file, header, err := r.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			s.renderMediaList(w, r, http.StatusRequestEntityTooLarge, s.uploadTooLargeMsg(r))
			return
		}
		s.renderMediaList(w, r, http.StatusUnprocessableEntity, s.tr(r, "Choose a file to upload."))
		return
	}
	defer file.Close()

	user := s.currentUser(r)
	folderID, _ := parseFolderParam(r.PostFormValue("folder"))
	md, err := s.deps.Media.UploadFrom(r.Context(), sanitizeFilename(header.Filename), file, header.Size, nil, user.ID, folderID)
	if err != nil {
		switch {
		case errors.Is(err, media.ErrTooManyPixels):
			s.renderMediaList(w, r, http.StatusRequestEntityTooLarge, s.tooManyPixelsMsg(r))
		case errors.Is(err, media.ErrTooLarge):
			s.renderMediaList(w, r, http.StatusRequestEntityTooLarge, s.uploadTooLargeMsg(r))
		case errors.Is(err, media.ErrUnsafeSVG):
			s.renderMediaList(w, r, http.StatusUnprocessableEntity, s.tr(r, unsafeSVGMsg))
		case errors.Is(err, media.ErrUnsupportedType), errors.Is(err, media.ErrUndecodable):
			s.renderMediaList(w, r, http.StatusUnprocessableEntity, s.tr(r, unsupportedTypeMsg))
		default:
			s.serverError(w, err)
		}
		return
	}

	alt := strings.TrimSpace(r.PostFormValue("alt"))
	if alt != "" {
		if err := s.deps.Media.UpdateAlt(r.Context(), md.ID, s.deps.DefaultLocale, alt); err != nil {
			s.serverError(w, err)
			return
		}
	}

	filed, err := s.fileInto(r.Context(), md, folderID)
	if err != nil {
		s.serverError(w, err)
		return
	}

	// Land on the tab that shows what was just uploaded.
	tab, msg := "images", "Image uploaded."
	switch md.Kind {
	case media.KindVideo:
		tab, msg = "videos", "Video uploaded."
	case media.KindFile:
		tab, msg = "documents", "Document uploaded."
	}
	if !filed {
		msg = "Uploaded, but saved outside the folder — that folder holds a different kind of file."
	}
	s.flash(r, s.tr(r, msg))
	http.Redirect(w, r, s.deps.AdminPath+"/media?tab="+tab, http.StatusSeeOther)
}

func (s *server) mediaUpdateAlt(w http.ResponseWriter, r *http.Request) {
	id, ok := s.mediaIDFromURL(w, r)
	if !ok {
		return
	}
	alt := strings.TrimSpace(r.PostFormValue("alt"))
	if err := s.deps.Media.UpdateAlt(r.Context(), id, s.deps.DefaultLocale, alt); err != nil {
		s.serverError(w, err)
		return
	}
	s.flash(r, s.tr(r, "Description saved."))
	s.backToMedia(w, r)
}

// mediaRename changes an item's display name. Nothing in the bucket
// moves, so every existing link keeps working; the media package keeps
// the stored extension whatever was submitted.
func (s *server) mediaRename(w http.ResponseWriter, r *http.Request) {
	id, ok := s.mediaIDFromURL(w, r)
	if !ok {
		return
	}
	_, err := s.deps.Media.Rename(r.Context(), id, s.deps.DefaultLocale, r.PostFormValue("name"))
	switch {
	case errors.Is(err, media.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, media.ErrBadFilename):
		s.flash(r, s.tr(r, "File names must be between 1 and 200 characters."))
		s.backToMedia(w, r)
		return
	case err != nil:
		s.serverError(w, err)
		return
	}
	s.flash(r, s.tr(r, "File renamed."))
	s.backToMedia(w, r)
}

// mediaDownload streams one item's original bytes as an attachment. The
// public URL cannot do this on its own: served from a bucket it is
// cross-origin, where the download attribute is ignored and the browser
// shows the file rather than saving it, and a private bucket is not
// reachable by the browser at all. Coming through here also puts the
// download behind the same permission gate as the rest of the library.
func (s *server) mediaDownload(w http.ResponseWriter, r *http.Request) {
	id, ok := s.mediaIDFromURL(w, r)
	if !ok {
		return
	}
	md, err := s.deps.Media.GetByID(r.Context(), id, s.deps.DefaultLocale)
	if errors.Is(err, media.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}

	body, contentType, err := s.deps.Media.OpenOriginal(r.Context(), md)
	if errors.Is(err, media.ErrObjectNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	defer body.Close()

	if contentType == "" {
		contentType = md.Mime
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	// Saved, not shown — which is the whole point of the route, and what
	// keeps an SVG or an HTML-ish document from rendering on this origin.
	w.Header().Set("Content-Disposition", attachmentDisposition(md.Filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Bytes from a private bucket behind a session: not for shared caches.
	w.Header().Set("Cache-Control", "private, no-store")
	// No Content-Length: the stored size is the upload's, and a header that
	// disagreed with the body by even a byte would truncate the download.
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, body); err != nil {
		s.deps.Logger.Debug("cms admin: media download interrupted", "id", md.ID, "err", err)
	}
}

// attachmentDisposition builds a Content-Disposition naming the saved
// file. Names are user-supplied, so it carries both forms: a quoted ASCII
// fallback with anything else flattened to "_", and the RFC 5987 UTF-8
// parameter every current browser prefers.
func attachmentDisposition(filename string) string {
	// sanitizeFilename keeps the last path element, and names an unnamed
	// upload "upload" — the same name it was stored under.
	name := sanitizeFilename(filename)
	ascii := make([]rune, 0, len(name))
	for _, r := range name {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			r = '_'
		}
		ascii = append(ascii, r)
	}
	return fmt.Sprintf("attachment; filename=\"%s\"; filename*=UTF-8''%s",
		string(ascii), url.PathEscape(name))
}

func (s *server) mediaDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := s.mediaIDFromURL(w, r)
	if !ok {
		return
	}
	err := s.deps.Media.Delete(r.Context(), id, s.deps.DefaultLocale)
	if errors.Is(err, media.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.flash(r, s.tr(r, "Media deleted."))
	s.backToMedia(w, r)
}

// mediaMove refiles one media item into a folder ("" form value = unfiled).
func (s *server) mediaMove(w http.ResponseWriter, r *http.Request) {
	id, ok := s.mediaIDFromURL(w, r)
	if !ok {
		return
	}
	folderID, _ := parseFolderParam(r.PostFormValue("folder"))
	if err := s.deps.Media.Move(r.Context(), id, folderID); err != nil {
		s.serverError(w, err)
		return
	}
	s.backToMedia(w, r)
}

// selectedIDs reads the media ids a bulk form submitted as repeated "id"
// fields, dropping anything unparseable rather than failing the whole
// action over one bad value.
func selectedIDs(r *http.Request) []int64 {
	if err := r.ParseForm(); err != nil {
		return nil
	}
	raw := r.PostForm["id"]
	ids := make([]int64, 0, len(raw))
	seen := make(map[int64]bool, len(raw))
	for _, v := range raw {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// maxBulk bounds one bulk action. Each item still costs work in the
// bucket — a manifest rewritten, objects deleted, an original streamed —
// so the selection a request can carry is capped rather than left to the
// request-size limit, which admits hundreds of thousands of ids. A page
// shows mediaPerPage items, so any selection made on the page fits.
const maxBulk = 500

// tooManySelected refuses a selection over maxBulk, back on the library
// with the reason, and reports whether it did.
func (s *server) tooManySelected(w http.ResponseWriter, r *http.Request, ids []int64) bool {
	if len(ids) <= maxBulk {
		return false
	}
	s.flash(r, fmt.Sprintf(s.tr(r, "Select at most %d files at a time."), maxBulk))
	s.backToMedia(w, r)
	return true
}

// mediaBulkMove refiles every selected item ("" form value = unfiled).
// The listing lets several items be selected at once, so its actions act
// on the selection rather than on one row at a time.
func (s *server) mediaBulkMove(w http.ResponseWriter, r *http.Request) {
	ids := selectedIDs(r)
	if s.tooManySelected(w, r, ids) {
		return
	}
	folderID, _ := parseFolderParam(r.PostFormValue("folder"))
	n, err := s.deps.Media.MoveMany(r.Context(), ids, folderID)
	if err != nil {
		s.serverError(w, err)
		return
	}
	if n == 1 {
		s.flash(r, s.tr(r, "1 file moved."))
	} else if n > 1 {
		s.flash(r, fmt.Sprintf(s.tr(r, "%d files moved."), n))
	}
	s.backToMedia(w, r)
}

// mediaBulkDelete deletes every selected item. A missing row is not an
// error: two tabs open on the same folder can both submit the same id.
func (s *server) mediaBulkDelete(w http.ResponseWriter, r *http.Request) {
	ids := selectedIDs(r)
	if s.tooManySelected(w, r, ids) {
		return
	}
	deleted := 0
	for _, id := range ids {
		err := s.deps.Media.Delete(r.Context(), id, s.deps.DefaultLocale)
		if errors.Is(err, media.ErrNotFound) {
			continue
		}
		if err != nil {
			s.serverError(w, err)
			return
		}
		deleted++
	}
	if deleted == 1 {
		s.flash(r, s.tr(r, "1 file deleted."))
	} else if deleted > 1 {
		s.flash(r, fmt.Sprintf(s.tr(r, "%d files deleted."), deleted))
	}
	s.backToMedia(w, r)
}

// mediaBulkDownload streams the selected items' originals as one zip,
// the multi-file counterpart of mediaDownload. The archive is written
// straight to the response as each object is read, so a selection of
// any size costs the server one object's buffer at a time and the save
// dialog appears before the last byte has been fetched.
//
// Entries are stored, not deflated: images, videos, and PDFs are already
// compressed, and deflating them again would spend CPU to make the file
// a few bytes larger. Two uploads with the same name are told apart by a
// numeric suffix, the way a desktop does it, so nothing is silently
// overwritten inside the archive.
//
// The rows are resolved before the first byte is written, so a selection
// with nothing behind it is still an honest 404. An object the bucket
// has lost after that point is skipped and logged — the status code has
// already gone out, and the other files are still worth having.
func (s *server) mediaBulkDownload(w http.ResponseWriter, r *http.Request) {
	ids := selectedIDs(r)
	if s.tooManySelected(w, r, ids) {
		return
	}
	found, err := s.deps.Media.ByIDs(r.Context(), ids, s.deps.DefaultLocale)
	if err != nil {
		s.serverError(w, err)
		return
	}
	items := make([]*media.Media, len(found))
	for i := range found {
		items[i] = &found[i]
	}
	if len(items) == 0 {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", attachmentDisposition(zipFilename(time.Now())))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method == http.MethodHead {
		return
	}

	zw := zip.NewWriter(w)
	names := map[string]bool{}
	for _, md := range items {
		body, _, err := s.deps.Media.OpenOriginal(r.Context(), md)
		if err != nil {
			s.deps.Logger.Warn("cms admin: media bulk download skipped an item", "id", md.ID, "err", err)
			continue
		}
		entry, err := zw.CreateHeader(&zip.FileHeader{
			Name:     uniqueZipName(names, sanitizeFilename(md.Filename)),
			Method:   zip.Store,
			Modified: md.CreatedAt,
		})
		if err == nil {
			_, err = io.Copy(entry, body)
		}
		body.Close()
		if err != nil {
			// Nothing more can reach a client that has gone away, and a
			// half-written entry cannot be taken back from one that hasn't.
			s.deps.Logger.Debug("cms admin: media bulk download interrupted", "id", md.ID, "err", err)
			return
		}
	}
	if err := zw.Close(); err != nil {
		s.deps.Logger.Debug("cms admin: media bulk download interrupted", "err", err)
	}
}

// zipFilename names a bulk download by when it was made, so two saved
// on the same day still land as different files.
func zipFilename(now time.Time) string {
	return "media-" + now.Format("2006-01-02-150405") + ".zip"
}

// uniqueZipName returns name, or name with " (2)", " (3)", ... before the
// extension when an earlier entry already took it, recording the result
// in taken. Compared case-insensitively: the archive may be unpacked on
// a filesystem that would otherwise fold two entries into one file.
func uniqueZipName(taken map[string]bool, name string) string {
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	candidate := name
	for n := 2; taken[strings.ToLower(candidate)]; n++ {
		candidate = fmt.Sprintf("%s (%d)%s", stem, n, ext)
	}
	taken[strings.ToLower(candidate)] = true
	return candidate
}

// mediaFolderCreate adds a folder from the admin media page, in the kind
// of the tab it was created on.
func (s *server) mediaFolderCreate(w http.ResponseWriter, r *http.Request) {
	kind := kindForTab(mediaTab(r.PostFormValue("tab")))
	_, err := s.deps.Media.CreateFolder(r.Context(), r.PostFormValue("name"), kind)
	if errors.Is(err, media.ErrDuplicateFolder) || errors.Is(err, media.ErrBadFolderName) {
		s.renderMediaList(w, r, http.StatusUnprocessableEntity, s.tr(r, friendlyFolderError(err)))
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.flash(r, s.tr(r, "Folder created."))
	s.backToMedia(w, r)
}

// mediaFolderDelete removes a folder, which the media package allows only
// while it is empty.
func (s *server) mediaFolderDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	err = s.deps.Media.DeleteFolder(r.Context(), id)
	if errors.Is(err, media.ErrFolderNotEmpty) {
		// The template disables the button for a folder with files in it,
		// so getting here means a stale page or a hand-made request. The
		// folder is still there, and so is the view it was deleted from.
		s.flash(r, s.tr(r, folderNotEmptyMsg))
		s.backToMedia(w, r)
		return
	}
	if err != nil {
		s.serverError(w, err)
		return
	}
	s.flash(r, s.tr(r, "Folder deleted."))
	// The Referer points into the folder that just vanished, so go back
	// to the root view on the tab the delete form was on instead.
	http.Redirect(w, r, s.deps.AdminPath+"/media?tab="+mediaTab(r.PostFormValue("tab")), http.StatusSeeOther)
}

func friendlyFolderError(err error) string {
	if errors.Is(err, media.ErrDuplicateFolder) {
		return "A folder with that name already exists."
	}
	return "Folder names must be between 1 and 60 characters."
}

func (s *server) mediaIDFromURL(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

// sanitizeFilename keeps just the final path element of a client-supplied
// filename, defensively.
func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if name == "" {
		name = "upload"
	}
	return name
}
