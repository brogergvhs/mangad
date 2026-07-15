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
	Label string
	Size  string
}

type volumesView struct {
	TitleID int64
	Rows    []volumeRowView
}

func (u *webUI) volumesFrag(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		u.fail(w, err)
		return
	}
	vols, err := u.svc.Volumes(r.Context(), id)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.frag(w, "volumesSection", volumesViewFrom(id, vols))
}

func volumesViewFrom(titleID int64, vols []library.Volume) volumesView {
	view := volumesView{TitleID: titleID}
	for _, v := range vols {
		label := strconv.FormatFloat(v.Number, 'f', -1, 64)
		if v.Number == 0 && v.Name != "" {
			label = ""
		}
		view.Rows = append(view.Rows, volumeRowView{Volume: v, Label: label, Size: util.Human(v.Bytes)})
	}
	return view
}

func (u *webUI) volumeRead(read bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r)
		if err != nil {
			u.fail(w, err)
			return
		}
		if err := u.svc.SetVolumeRead(r.Context(), id, read); err != nil {
			u.fail(w, err)
			return
		}
		vol, err := u.svc.GetVolume(r.Context(), id)
		if err != nil {
			u.fail(w, err)
			return
		}
		vols, err := u.svc.Volumes(r.Context(), vol.TitleID)
		if err != nil {
			u.fail(w, err)
			return
		}
		u.frag(w, "volumesSection", volumesViewFrom(vol.TitleID, vols))
	}
}

// volumeCover serves the custom cover when set, else the volume's first page.
func (u *webUI) volumeCover(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	blob, mime, err := u.svc.VolumeCover(r.Context(), id)
	if err == nil && len(blob) > 0 {
		w.Header().Set("Content-Type", mime)
		w.Header().Set("Cache-Control", "private, max-age=3600")
		_, _ = w.Write(blob)
		return
	}
	vol, err := u.svc.GetVolume(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	entry, rc, err := cbzPage(vol.File, 1)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", imageMime(entry.Name))
	w.Header().Set("Cache-Control", "private, max-age=3600")
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
	vols, err := u.svc.Volumes(r.Context(), vol.TitleID)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.frag(w, "volumesSection", volumesViewFrom(vol.TitleID, vols))
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
	w.Header().Set("HX-Redirect", fmt.Sprintf("/library/%d", title.ID))
}
