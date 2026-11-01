package main

import "net/http"

// Release builds set this to the installer version through -ldflags.
var engineVersion = "development"

func (a *app) getVersion(w http.ResponseWriter, r *http.Request) {
	respond(w, http.StatusOK, map[string]string{"version": engineVersion})
}
