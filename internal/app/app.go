// Package app wires the whole HTTP API together.
//
// The automatic tests talk to your API ONLY through NewRouter(), so you are free to
// organise the rest of the code (model, repository, handler, middleware) as you like.
package app

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	_ "homework/docs"
	"homework/internal/apperr"
	"homework/internal/auth"
	"homework/internal/chat"
	"homework/internal/handler"
	webhandler "homework/internal/handler/web"
	"homework/internal/middleware"
	"homework/internal/repository"
	"homework/internal/service"
	webui "homework/web"
)

type Deps struct {
	Movies        repository.MovieRepository
	Catalog       *service.CatalogService
	Users         repository.UserRepository
	Tokens        *auth.TokenManager
	EnableSwagger bool
	Secure        bool
	CookieSecure  bool
	WebUIEnabled  *bool
	// Chat enables the movie chat when set; the caller starts the hub with go Chat.Run(ctx).
	Chat               *chat.Hub
	ChatAllowedOrigins []string
	// TrailerStorage enables the trailer API when set; Trailers defaults to an in-memory repository.
	TrailerStorage service.TrailerStorage
	Trailers       repository.TrailerRepository
}

// trailerUploadRoute is exempt from the global body limit; the handler applies its own.
const trailerUploadRoute = "PUT /api/v1/movies/:id/trailer"

// NewRouter creates a router and a fresh in-memory repository for each call.
func NewRouter() http.Handler {
	return newRouter(unprotectedDeps(repository.NewInMemoryMovieRepositoryAdapter(), false))
}

// NewRouterWithSwagger enables the documentation UI for the executable server.
// Contract tests should continue to use NewRouter.
func NewRouterWithSwagger() http.Handler {
	return newRouter(unprotectedDeps(repository.NewInMemoryMovieRepositoryAdapter(), true))
}

func NewRouterWithRepository(movieRepository repository.MovieRepository) http.Handler {
	return newRouter(unprotectedDeps(movieRepository, true))
}

func NewRouterWithDeps(deps Deps) http.Handler {
	deps.Secure = true
	if deps.WebUIEnabled == nil {
		enabled := true
		deps.WebUIEnabled = &enabled
	}
	if deps.Movies == nil {
		deps.Movies = repository.NewInMemoryMovieRepositoryAdapter()
	}
	if deps.Users == nil {
		deps.Users = repository.NewInMemoryUserRepository()
	}
	return newRouter(deps)
}

func unprotectedDeps(movieRepository repository.MovieRepository, enableSwagger bool) Deps {
	tokens, err := auth.NewTokenManager("unprotected-in-memory-jwt-secret-32b", 24*time.Hour)
	if err != nil {
		panic(err)
	}
	return Deps{
		Movies:        movieRepository,
		Users:         repository.NewInMemoryUserRepository(),
		Tokens:        tokens,
		EnableSwagger: enableSwagger,
		Secure:        false,
	}
}

func newRouter(deps Deps) http.Handler {
	if deps.Catalog == nil {
		deps.Catalog = service.NewCatalogService(repository.NewMovieSearcher(deps.Movies))
	}
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	// CORS must wrap recovery so panic responses also receive CORS headers.
	router.Use(middleware.CORS(), middleware.Recovery(), middleware.RequestLogger(), middleware.RequestBodyLimit(trailerUploadRoute))
	router.HandleMethodNotAllowed = true
	router.RedirectTrailingSlash = false
	router.RedirectFixedPath = false

	authService := service.NewAuthService(deps.Users, deps.Tokens)
	var trailerService *service.TrailerService
	movieService := service.NewMovieService(deps.Movies)
	if deps.Secure && deps.TrailerStorage != nil {
		if deps.Trailers == nil {
			deps.Trailers = repository.NewInMemoryTrailerRepository()
		}
		trailerService = service.NewTrailerService(deps.Movies, deps.Trailers, deps.TrailerStorage)
		movieService = service.NewMovieServiceWithTrailerCleanup(deps.Movies, trailerService)
	}
	movieHandler := handler.NewMovieHandler(movieService)
	authHandler := handler.NewAuthHandler(authService)
	userHandler := handler.NewUserHandler(service.NewUserService(deps.Users))
	var catalogHandler *webhandler.CatalogHandler
	var renderer *webhandler.Renderer
	var staticFS http.FileSystem
	var err error
	if deps.Secure && *deps.WebUIEnabled {
		renderer, err = webhandler.NewRenderer()
		if err != nil {
			panic(fmt.Errorf("initialize web templates: %w", err))
		}
		files, err := webui.StaticFS()
		if err != nil {
			panic(fmt.Errorf("initialize web static files: %w", err))
		}
		staticFS = http.FS(files)
		catalogHandler = webhandler.NewCatalogHandler(deps.Catalog, movieService, renderer, deps.Chat != nil)
	}

	router.GET("/health", handler.Health)
	api := router.Group("/api/v1")
	if deps.Secure {
		public := api.Group("")
		protected := api.Group("", middleware.AuthMiddleware(deps.Tokens, deps.Users))
		staff := protected.Group("", middleware.RequireRole("editor", "admin"))
		admin := protected.Group("", middleware.RequireRole("admin"))
		handler.RegisterAPI(public, protected, staff, admin, movieHandler, authHandler, userHandler)
		if trailerService != nil {
			handler.RegisterTrailerAPI(public, staff, handler.NewTrailerHandler(trailerService))
		}
	} else {
		handler.RegisterAPI(api, api, api, api, movieHandler, authHandler, userHandler)
	}
	if deps.EnableSwagger {
		// Swagger 2 applies BasePath to every path, so this alias serves its health operation.
		router.GET("/api/v1/health", handler.Health)
		router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler, ginSwagger.URL("/swagger/doc.json")))
	}
	if deps.Secure && deps.Chat != nil {
		router.GET("/ws/movies/:id", chat.NewHandler(deps.Chat, deps.Tokens, deps.Users, movieService, deps.ChatAllowedOrigins).Serve)
	}
	if catalogHandler != nil {
		staticRoutes := router.Group("", webSecurityHeaders())
		staticRoutes.GET("/static/*filepath", gin.WrapH(http.StripPrefix("/static/", http.FileServer(staticFS))))
		staticRoutes.GET("/", catalogHandler.Root)

		webAuthHandler := webhandler.NewAuthHandler(authService, renderer, deps.CookieSecure)
		webRoutes := router.Group("")
		webRoutes.Use(webSecurityHeaders(), webhandler.CSRF(deps.CookieSecure, renderer),
			webhandler.OptionalAuth(deps.Tokens, deps.Users, deps.CookieSecure))
		webRoutes.GET("/catalog", catalogHandler.Catalog)
		webRoutes.GET("/web/movies", catalogHandler.Movies)
		webRoutes.GET("/login", webAuthHandler.LoginPage)
		webRoutes.POST("/login", webAuthHandler.Login)
		webRoutes.POST("/logout", webhandler.RequireLogin(), webAuthHandler.Logout)
		if deps.Chat != nil {
			webRoutes.GET("/movies/:id/chat", webhandler.RequireLogin(), catalogHandler.ChatPage)
		}
		editorWebRoutes := webRoutes.Group("", webhandler.RequireRole(renderer, "editor", "admin"))
		editorWebRoutes.GET("/movies/new", catalogHandler.NewMoviePage)
		editorWebRoutes.POST("/movies/new", catalogHandler.CreateMovie)
		editorWebRoutes.GET("/movies/:id/edit", catalogHandler.EditMoviePage)
		editorWebRoutes.POST("/movies/:id/edit", catalogHandler.UpdateMovie)
		adminWebRoutes := webRoutes.Group("", webhandler.RequireRole(renderer, "admin"))
		adminWebRoutes.DELETE("/web/movies/:id", catalogHandler.DeleteMovie)
		adminWebRoutes.GET("/register", webAuthHandler.RegisterPage)
		adminWebRoutes.POST("/register", webAuthHandler.Register)
	}
	router.NoRoute(func(c *gin.Context) {
		apperr.WriteError(c, apperr.HTTPError{Status: http.StatusNotFound, Code: "not_found", Message: "Route not found"})
	})
	router.NoMethod(func(c *gin.Context) {
		apperr.WriteError(c, apperr.HTTPError{Status: http.StatusMethodNotAllowed, Code: "not_found", Message: "Method not allowed"})
	})
	return router
}

func webSecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "same-origin")
		c.Next()
	}
}
