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

// notificationScope bounds visibility to the acting user's permissions.
func notificationScope(user *auth.User) service.NotificationScope {
	return service.NotificationScope{
		UserID: user.ID,
		Server: user.Can(auth.PermJobsView),
		All:    user.Can(auth.PermUsersManage),
	}
}

func (u *webUI) notificationsCard(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	items, _ := u.svc.Notifications(r.Context(), notificationScope(user), 50)
	u.frag(w, "notificationsCard", notificationsView{Items: items, CanManage: user.Can(auth.PermJobsManage)})
}

func (u *webUI) notificationsBadge(w http.ResponseWriter, r *http.Request) {
	n, _ := u.svc.UnreadNotificationCount(r.Context(), notificationScope(userFrom(r.Context())))
	u.frag(w, "notificationsBadge", n)
}

func (u *webUI) notificationsRead(w http.ResponseWriter, r *http.Request) {
	if err := u.svc.MarkNotificationsRead(r.Context(), notificationScope(userFrom(r.Context()))); err != nil {
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
