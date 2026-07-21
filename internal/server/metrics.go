package server

import (
	"net/http"
	"strconv"

	"github.com/brogergvhs/kaodoku/internal/auth"
	"github.com/brogergvhs/kaodoku/internal/service"
)

var weekdayNames = [7]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

type personalVM struct {
	M           service.PersonalMetrics
	HourBars    []service.NamedCount
	WeekdayBars []service.NamedCount
}

type metricsView struct {
	User         *auth.User
	Days         int
	ShowPersonal bool
	ShowOverview bool
	P            personalVM
}

type overviewView struct {
	User *auth.User
	M    service.OverviewMetrics
}

// metricsDays clamps the window selector to the supported values.
func metricsDays(r *http.Request) int {
	switch r.URL.Query().Get("days") {
	case "90":
		return 90
	case "365":
		return 365
	default:
		return 30
	}
}

func (u *webUI) buildPersonal(r *http.Request) (personalVM, error) {
	user := userFrom(r.Context())
	m, err := u.svc.PersonalMetrics(r.Context(), user.ID, metricsDays(r))
	if err != nil {
		return personalVM{}, err
	}
	vm := personalVM{M: m}
	for h := 0; h < 24; h++ {
		vm.HourBars = append(vm.HourBars, service.NamedCount{Name: strconv.Itoa(h), Count: m.HourHist[h]})
	}
	for w := 0; w < 7; w++ {
		vm.WeekdayBars = append(vm.WeekdayBars, service.NamedCount{Name: weekdayNames[w], Count: m.WeekdayHist[w]})
	}
	return vm, nil
}

func (u *webUI) metricsPage(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	data := metricsView{
		User:         user,
		Days:         metricsDays(r),
		ShowPersonal: user.Can(auth.PermReaderUse),
		ShowOverview: user.Can(auth.PermStatsView),
	}
	if data.ShowPersonal {
		p, err := u.buildPersonal(r)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		data.P = p
	}
	u.page(w, r, "metrics", "Metrics", data)
}

// metricsPersonal re-renders just the personal section (window selector swap).
func (u *webUI) metricsPersonal(w http.ResponseWriter, r *http.Request) {
	p, err := u.buildPersonal(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	u.frag(w, "metricsPersonal", metricsView{User: userFrom(r.Context()), Days: metricsDays(r), P: p})
}

func (u *webUI) metricsOverview(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	m, err := u.svc.OverviewMetrics(r.Context(), user.AllowAdult)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	u.frag(w, "metricsOverview", overviewView{User: user, M: m})
}
