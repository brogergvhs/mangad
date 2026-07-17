package server

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/brogergvhs/mangad/internal/library"
	"github.com/brogergvhs/mangad/internal/util"
)

type volumeRowView struct {
	library.Volume
	Label   string
	Size    string
	Percent int
	ReadTip string
}

func volumeRows(vols []library.Volume) []volumeRowView {
	out := make([]volumeRowView, 0, len(vols))
	for _, v := range vols {
		label := strconv.FormatFloat(v.Number, 'f', -1, 64)
		if v.Number == 0 && v.Name != "" {
			label = ""
		}
		row := volumeRowView{Volume: v, Label: label, Size: util.Human(v.Bytes)}
		switch {
		case v.Read:
			row.Percent = 100
		case v.Pages > 0:
			row.Percent = v.ReadPages * 100 / v.Pages
		}
		if v.Pages > 0 {
			row.ReadTip = fmt.Sprintf("%d/%d pages read", v.ReadPages, v.Pages)
			if v.Read {
				row.ReadTip = fmt.Sprintf("%d/%d pages read", v.Pages, v.Pages)
			}
		}
		out = append(out, row)
	}
	return out
}

func (u *webUI) volumeRead(read bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			u.fail(w, err)
			return
		}
		vol, err := u.svc.GetVolume(r.Context(), id)
		if err != nil || !titleAllowed(r.Context(), u.svc, vol.TitleID) {
			http.NotFound(w, r)
			return
		}
		if err := u.svc.SetVolumeRead(r.Context(), id, read); err != nil {
			u.fail(w, err)
			return
		}
		u.writeTitleContent(w, r, vol.TitleID, "volumes")
	}
}

func (u *webUI) volumesRange(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	if !titleAllowed(r.Context(), u.svc, id) {
		http.NotFound(w, r)
		return
	}
	from, err1 := strconv.ParseFloat(r.FormValue("from"), 64)
	to, err2 := strconv.ParseFloat(r.FormValue("to"), 64)
	if err1 != nil || err2 != nil {
		u.fail(w, fmt.Errorf("enter volume numbers"))
		return
	}
	if _, err := u.svc.SetVolumeRangeRead(r.Context(), id, from, to, r.FormValue("action") == "read"); err != nil {
		u.fail(w, err)
		return
	}
	u.writeTitleContent(w, r, id, "volumes")
}

// volumeCover serves the custom cover, else the pre-generated thumbnail,
// else the raw first page. URLs carry a version query so responses can be
// cached hard; uploads and resets change the version.
func (u *webUI) volumeCover(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	vol, err := u.svc.GetVolume(r.Context(), id)
	if err != nil || !titleAllowed(r.Context(), u.svc, vol.TitleID) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=604800")
	if blob, mime, err := u.svc.VolumeCover(r.Context(), id); err == nil && len(blob) > 0 {
		w.Header().Set("Content-Type", mime)
		_, _ = w.Write(blob)
		return
	}
	if blob, mime, err := u.svc.VolumeThumb(r.Context(), id); err == nil && len(blob) > 0 {
		w.Header().Set("Content-Type", mime)
		_, _ = w.Write(blob)
		return
	}
	entry, rc, err := cbzPage(vol.File, 1)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", imageMime(entry.Name))
	_, _ = io.Copy(w, rc)
}

func imageMime(name string) string {
	switch strings.ToLower(name[strings.LastIndex(name, ".")+1:]) {
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	case "avif":
		return "image/avif"
	default:
		return "image/jpeg"
	}
}

const maxCoverBytes = 5 << 20

func (u *webUI) volumeCoverUpload(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	if vol, err := u.svc.GetVolume(r.Context(), id); err != nil || !titleAllowed(r.Context(), u.svc, vol.TitleID) {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseMultipartForm(maxCoverBytes); err != nil {
		u.fail(w, fmt.Errorf("cover upload too large or malformed"))
		return
	}
	file, header, err := r.FormFile("cover")
	if err != nil {
		u.fail(w, fmt.Errorf("no cover file selected"))
		return
	}
	defer file.Close()
	blob, err := io.ReadAll(io.LimitReader(file, maxCoverBytes))
	if err != nil {
		u.fail(w, err)
		return
	}
	mime := header.Header.Get("Content-Type")
	if !strings.HasPrefix(mime, "image/") {
		mime = http.DetectContentType(blob)
		if !strings.HasPrefix(mime, "image/") {
			u.fail(w, fmt.Errorf("cover must be an image"))
			return
		}
	}
	if err := u.svc.SetVolumeCover(r.Context(), id, blob, mime); err != nil {
		u.fail(w, err)
		return
	}
	u.volumeSectionByVolume(w, r, id)
}

func (u *webUI) volumeCoverReset(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	if vol, err := u.svc.GetVolume(r.Context(), id); err != nil || !titleAllowed(r.Context(), u.svc, vol.TitleID) {
		http.NotFound(w, r)
		return
	}
	if err := u.svc.SetVolumeCover(r.Context(), id, nil, ""); err != nil {
		u.fail(w, err)
		return
	}
	u.volumeSectionByVolume(w, r, id)
}

func (u *webUI) volumeSectionByVolume(w http.ResponseWriter, r *http.Request, volumeID int64) {
	vol, err := u.svc.GetVolume(r.Context(), volumeID)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.writeTitleContent(w, r, vol.TitleID, "volumes")
}

func (u *webUI) importAttachVolumes(w http.ResponseWriter, r *http.Request) {
	titleID, err := strconv.ParseInt(r.FormValue("title_id"), 10, 64)
	if err != nil {
		u.fail(w, fmt.Errorf("pick a target title"))
		return
	}
	title, err := u.svc.AttachVolumesFolder(r.Context(), r.FormValue("folder"), titleID)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.kick()
	w.Header().Set("HX-Redirect", fmt.Sprintf("/library/%d", title.ID))
}
