package routes

import (
	"net/http"

	lib "github.com/quackdiscord/bot/api/lib"
	"github.com/quackdiscord/bot/storage"
)

func stats(w http.ResponseWriter, r *http.Request) {
	stats, err := storage.GetLatestStats()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	lib.JSONResponse(w, stats)
}
