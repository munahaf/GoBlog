package main

import (
	"fmt"
	"net/http"
)

func serveRobotsTXT(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf("User-agent: *\nSitemap: %v", appConfig.Server.PublicAddress+sitemapPath)))
}
