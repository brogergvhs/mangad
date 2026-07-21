package server

import (
	"net/http"

	"github.com/brogergvhs/kaodoku/internal/auth"
	"github.com/brogergvhs/kaodoku/internal/service"
)

type notificationsView struct {
	Items     []service.Notification
	CanManage bool
}

func (u *webUI) notificationsCard(w http.ResponseWriter, r *http.Request) {
	items, _ := u.svc.Notifications(r.Context(), 50)
	canManage := userFrom(r.Context()).Can(auth.PermJobsManage)
	u.frag(w, "notificationsCard", notificationsView{Items: items, CanManage: canManage})
}

func (u *webUI) notificationsBadge(w http.ResponseWriter, r *http.Request) {
	n, _ := u.svc.UnreadNotificationCount(r.Context())
	u.frag(w, "notificationsBadge", n)
}

func (u *webUI) notificationsRead(w http.ResponseWriter, r *http.Request) {
	if err := u.svc.MarkNotificationsRead(r.Context()); err != nil {
		u.fail(w, err)
		return
	}
	u.notificationsCard(w, r)
}

func (u *webUI) notificationsClear(w http.ResponseWriter, r *http.Request) {
	if err := u.svc.ClearNotifications(r.Context()); err != nil {
		u.fail(w, err)
		return
	}
	u.notificationsCard(w, r)
}

func (u *webUI) notificationDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64Path(r, "id")
	if err != nil {
		u.fail(w, err)
		return
	}
	if err := u.svc.DeleteNotification(r.Context(), id); err != nil {
		u.fail(w, err)
		return
	}
	u.notificationsCard(w, r)
}
