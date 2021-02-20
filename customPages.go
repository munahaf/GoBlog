package main

import "net/http"

func serveCustomPage(blog *configBlog, page *customPage) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if appConfig.Cache != nil && appConfig.Cache.Enable && page.Cache {
			if page.CacheExpiration != 0 {
				setInternalCacheExpirationHeader(w, page.CacheExpiration)
			} else {
				setInternalCacheExpirationHeader(w, int(appConfig.Cache.Expiration))
			}
		}
		render(w, r, page.Template, &renderData{
			Blog:      blog,
			Canonical: appConfig.Server.PublicAddress + page.Path,
			Data:      page.Data,
		})
	}
}
