package main

import (
	"compress/flate"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/go-chi/chi"
	"github.com/go-chi/chi/middleware"
	"golang.org/x/crypto/acme/autocert"
)

const (
	contentType = "Content-Type"

	charsetUtf8Suffix = "; charset=utf-8"

	contentTypeHTML          = "text/html"
	contentTypeJSON          = "application/json"
	contentTypeWWWForm       = "application/x-www-form-urlencoded"
	contentTypeMultipartForm = "multipart/form-data"
	contentTypeAS            = "application/activity+json"

	contentTypeHTMLUTF8 = contentTypeHTML + charsetUtf8Suffix
	contentTypeJSONUTF8 = contentTypeJSON + charsetUtf8Suffix
	contentTypeASUTF8   = contentTypeAS + charsetUtf8Suffix
)

var (
	d *dynamicHandler
)

func startServer() (err error) {
	d = &dynamicHandler{}
	err = reloadRouter()
	if err != nil {
		return
	}
	localAddress := ":" + strconv.Itoa(appConfig.Server.Port)
	if appConfig.Server.PublicHTTPS {
		cache, err := newAutocertCache()
		if err != nil {
			return err
		}
		certManager := autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(appConfig.Server.Domain),
			Cache:      cache,
			Email:      appConfig.Server.LetsEncryptMail,
		}
		tlsConfig := certManager.TLSConfig()
		server := http.Server{
			Addr:      ":https",
			Handler:   securityHeaders(d),
			TLSConfig: tlsConfig,
		}
		go http.ListenAndServe(":http", certManager.HTTPHandler(nil))
		err = server.ListenAndServeTLS("", "")
	} else if appConfig.Server.LocalHTTPS {
		err = http.ListenAndServeTLS(localAddress, "https/server.crt", "https/server.key", d)
	} else {
		err = http.ListenAndServe(localAddress, d)
	}
	return
}

func reloadRouter() error {
	h, err := buildHandler()
	if err != nil {
		return err
	}
	d.swapHandler(h)
	return nil
}

func buildHandler() (http.Handler, error) {
	r := chi.NewRouter()

	r.Use(middleware.Recoverer)
	if appConfig.Server.Logging {
		r.Use(middleware.RealIP)
		r.Use(middleware.RequestLogger(&middleware.DefaultLogFormatter{
			Logger:  log.New(os.Stdout, "", log.LstdFlags),
			NoColor: true,
		}))
	}
	r.Use(middleware.Compress(flate.DefaultCompression))
	r.Use(middleware.StripSlashes)
	r.Use(middleware.GetHead)
	if !appConfig.Cache.Enable {
		r.Use(middleware.NoCache)
	}

	// Profiler
	if appConfig.Server.Debug {
		r.Mount("/debug", middleware.Profiler())
	}

	authMiddleware := middleware.BasicAuth("API", map[string]string{
		appConfig.User.Nick: appConfig.User.Password,
	})

	// API
	r.Route("/api", func(apiRouter chi.Router) {
		apiRouter.Use(middleware.NoCache, authMiddleware)
		apiRouter.Post("/hugo", apiPostCreateHugo)
	})

	// Micropub
	r.Route(micropubPath, func(mpRouter chi.Router) {
		mpRouter.Use(middleware.NoCache, checkIndieAuth)
		mpRouter.Get("/", serveMicropubQuery)
		mpRouter.Post("/", serveMicropubPost)
		if appConfig.Micropub.MediaStorage != nil {
			mpRouter.Post(micropubMediaSubPath, serveMicropubMedia)
		}
	})

	// IndieAuth
	r.Route("/indieauth", func(indieauthRouter chi.Router) {
		indieauthRouter.Use(middleware.NoCache)
		indieauthRouter.With(authMiddleware).Get("/", indieAuthAuth)
		indieauthRouter.With(authMiddleware).Post("/accept", indieAuthAccept)
		indieauthRouter.Post("/", indieAuthAuth)
		indieauthRouter.Get("/token", indieAuthToken)
		indieauthRouter.Post("/token", indieAuthToken)
	})

	// ActivityPub and stuff
	if appConfig.ActivityPub.Enabled {
		r.Post("/activitypub/inbox/{blog}", apHandleInbox)
		r.Get("/.well-known/webfinger", apHandleWebfinger)
		r.Get("/.well-known/host-meta", handleWellKnownHostMeta)
	}

	// Posts
	allPostPaths, err := allPostPaths()
	if err != nil {
		return nil, err
	}
	var postMW []func(http.Handler) http.Handler
	if appConfig.ActivityPub.Enabled {
		postMW = []func(http.Handler) http.Handler{manipulateAsPath, cacheMiddleware, minifier.Middleware}
	} else {
		postMW = []func(http.Handler) http.Handler{cacheMiddleware, minifier.Middleware}
	}
	for _, path := range allPostPaths {
		if path != "" {
			r.With(postMW...).Get(path, servePost)
		}
	}

	// Redirects
	allRedirectPaths, err := allRedirectPaths()
	if err != nil {
		return nil, err
	}
	for _, path := range allRedirectPaths {
		if path != "" {
			r.With(cacheMiddleware, minifier.Middleware).Get(path, serveRedirect)
		}
	}

	// Assets
	for _, path := range allAssetPaths() {
		r.Get(path, serveAsset)
	}

	paginationPath := "/page/{page:[0-9-]+}"
	feedPath := ".{feed:rss|json|atom}"

	for blog, blogConfig := range appConfig.Blogs {

		fullBlogPath := blogConfig.Path
		blogPath := fullBlogPath
		if blogPath == "/" {
			blogPath = ""
		}

		// Indexes, Feeds
		for _, section := range blogConfig.Sections {
			if section.Name != "" {
				path := blogPath + "/" + section.Name
				r.With(cacheMiddleware, minifier.Middleware).Get(path, serveSection(blog, path, section))
				r.With(cacheMiddleware, minifier.Middleware).Get(path+feedPath, serveSection(blog, path, section))
				r.With(cacheMiddleware, minifier.Middleware).Get(path+paginationPath, serveSection(blog, path, section))
			}
		}

		for _, taxonomy := range blogConfig.Taxonomies {
			if taxonomy.Name != "" {
				path := blogPath + "/" + taxonomy.Name
				r.With(cacheMiddleware, minifier.Middleware).Get(path, serveTaxonomy(blog, taxonomy))
				values, err := allTaxonomyValues(blog, taxonomy.Name)
				if err != nil {
					return nil, err
				}
				for _, tv := range values {
					vPath := path + "/" + urlize(tv)
					r.With(cacheMiddleware, minifier.Middleware).Get(vPath, serveTaxonomyValue(blog, vPath, taxonomy, tv))
					r.With(cacheMiddleware, minifier.Middleware).Get(vPath+feedPath, serveTaxonomyValue(blog, vPath, taxonomy, tv))
					r.With(cacheMiddleware, minifier.Middleware).Get(vPath+paginationPath, serveTaxonomyValue(blog, vPath, taxonomy, tv))
				}
			}
		}

		// Photos
		if blogConfig.Photos.Enabled {
			r.With(cacheMiddleware, minifier.Middleware).Get(blogPath+blogConfig.Photos.Path, servePhotos(blog))
			r.With(cacheMiddleware, minifier.Middleware).Get(blogPath+blogConfig.Photos.Path+paginationPath, servePhotos(blog))
		}

		// Blog
		var mw []func(http.Handler) http.Handler
		if appConfig.ActivityPub.Enabled {
			mw = []func(http.Handler) http.Handler{manipulateAsPath, cacheMiddleware, minifier.Middleware}
		} else {
			mw = []func(http.Handler) http.Handler{cacheMiddleware, minifier.Middleware}
		}
		r.With(mw...).Get(fullBlogPath, serveHome(blog, blogPath))
		r.With(cacheMiddleware, minifier.Middleware).Get(fullBlogPath+feedPath, serveHome(blog, blogPath))
		r.With(cacheMiddleware, minifier.Middleware).Get(blogPath+paginationPath, serveHome(blog, blogPath))

		// Custom pages
		for _, cp := range blogConfig.CustomPages {
			serveFunc := serveCustomPage(blogConfig, cp)
			if cp.Cache {
				r.With(cacheMiddleware, minifier.Middleware).Get(cp.Path, serveFunc)
			} else {
				r.With(minifier.Middleware).Get(cp.Path, serveFunc)
			}
		}
	}

	// Sitemap
	r.With(cacheMiddleware, minifier.Middleware).Get(sitemapPath, serveSitemap)

	// Check redirects, then serve 404
	r.With(checkRegexRedirects, cacheMiddleware, minifier.Middleware).NotFound(serve404)

	return r, nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Strict-Transport-Security", "max-age=31536000;")
		w.Header().Add("Referrer-Policy", "no-referrer")
		w.Header().Add("X-Content-Type-Options", "nosniff")
		w.Header().Add("X-Frame-Options", "SAMEORIGIN")
		w.Header().Add("X-Xss-Protection", "1; mode=block")
		// TODO: Add CSP
		next.ServeHTTP(w, r)
	})
}

type dynamicHandler struct {
	realHandler atomic.Value
}

func (d *dynamicHandler) swapHandler(h http.Handler) {
	d.realHandler.Store(h)
}

func (d *dynamicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.realHandler.Load().(http.Handler).ServeHTTP(w, r)
}

func slashTrimmedPath(r *http.Request) string {
	return trimSlash(r.URL.Path)
}

func trimSlash(s string) string {
	if len(s) > 1 {
		s = strings.TrimSuffix(s, "/")
	}
	return s
}
