package server

import (
	"net/http"
	"strconv"

	"github.com/brogergvhs/mangad/internal/auth"
)

type usersView struct {
	Users []auth.User
	Roles []auth.Role
	Perms []string
}

func (u *webUI) usersData(r *http.Request) usersView {
	users, _ := u.svc.Auth().ListUsers(r.Context())
	roles, _ := u.svc.Auth().ListRoles(r.Context())
	return usersView{Users: users, Roles: roles, Perms: auth.Permissions()}
}

func (u *webUI) usersPage(w http.ResponseWriter, r *http.Request) {
	u.page(w, r, "users", "Users", u.usersData(r))
}

func (u *webUI) usersFrag(w http.ResponseWriter, r *http.Request) {
	u.frag(w, "usersContent", u.usersData(r))
}

func (u *webUI) userCreate(w http.ResponseWriter, r *http.Request) {
	roleID, _ := strconv.ParseInt(r.FormValue("role_id"), 10, 64)
	if err := u.svc.Auth().CreateUser(r.Context(), r.FormValue("username"), r.FormValue("password"), roleID); err != nil {
		u.fail(w, err)
		return
	}
	u.usersFrag(w, r)
}

func (u *webUI) userUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64Path(r, "id")
	if err != nil {
		u.fail(w, err)
		return
	}
	roleID, _ := strconv.ParseInt(r.FormValue("role_id"), 10, 64)
	if err := u.svc.Auth().UpdateUser(r.Context(), id, roleID, r.FormValue("password")); err != nil {
		u.fail(w, err)
		return
	}
	u.usersFrag(w, r)
}

func (u *webUI) userDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64Path(r, "id")
	if err != nil {
		u.fail(w, err)
		return
	}
	if err := u.svc.Auth().DeleteUser(r.Context(), id); err != nil {
		u.fail(w, err)
		return
	}
	u.usersFrag(w, r)
}

func (u *webUI) roleSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		u.fail(w, err)
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err := u.svc.Auth().SaveRole(r.Context(), id, r.FormValue("name"), r.Form["perm"]); err != nil {
		u.fail(w, err)
		return
	}
	u.usersFrag(w, r)
}

func (u *webUI) roleDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseInt64Path(r, "id")
	if err != nil {
		u.fail(w, err)
		return
	}
	if err := u.svc.Auth().DeleteRole(r.Context(), id); err != nil {
		u.fail(w, err)
		return
	}
	u.usersFrag(w, r)
}
