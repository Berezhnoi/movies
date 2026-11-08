package web

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	apihandler "homework/internal/handler"
	"homework/internal/middleware"
	"homework/internal/service"
)

// TrailerPage renders the watch page. A lost object shows "Trailer unavailable"
// instead of a broken player.
func (handler *CatalogHandler) TrailerPage(c *gin.Context) {
	id, err := service.ParseMovieID(c.Param("id"))
	if err != nil {
		handler.renderFormError(c, http.StatusBadRequest, "Bad Request", "Please provide a valid movie ID.")
		return
	}
	movie, err := handler.movies.GetByIDWithError(c.Request.Context(), id)
	if err != nil {
		handler.renderMovieLookupError(c, err)
		return
	}
	page := TrailerPage{
		BasePage:   BasePage{Title: "Trailer - " + movie.Title, User: userView(c)},
		MovieID:    id,
		MovieTitle: movie.Title,
	}
	_, err = handler.trailers.Metadata(c.Request.Context(), id)
	var unavailable service.TrailerUnavailableError
	var notFound service.NotFoundError
	switch {
	case err == nil:
		handler.renderer.Page(c, http.StatusOK, "trailer", page)
	case errors.As(err, &unavailable):
		page.Unavailable = true
		handler.renderer.Page(c, http.StatusNotFound, "trailer", page)
	case errors.As(err, &notFound):
		handler.renderFormError(c, http.StatusNotFound, "Not Found", "This movie has no trailer.")
	case errors.Is(err, service.ErrStorageUnavailable):
		handler.renderFormError(c, http.StatusServiceUnavailable, "Service unavailable", "Trailer storage is unavailable. Please try again later.")
	default:
		log.Printf("load web trailer: %v", err)
		handler.renderFormError(c, http.StatusInternalServerError, "Internal server error", "Please try again later.")
	}
}

// UploadTrailer handles the HTMX upload from the edit form and returns the
// refreshed trailer section. It shares the API's size limit and multipart
// parsing. Validation errors keep their 4xx status; app.js
// swaps the section for them so the message is shown in place.
func (handler *CatalogHandler) UploadTrailer(c *gin.Context) {
	id, ok := handler.trailerMovieID(c)
	if !ok {
		return
	}
	userID, _ := middleware.UserIDFrom(c)
	_, _, err := apihandler.ReceiveTrailer(c, handler.trailers, id, uint64(userID))
	if err != nil {
		status, message := trailerUploadFailure(err)
		if status == http.StatusInternalServerError {
			log.Printf("upload web trailer: movie_id=%d: %v", id, err)
		}
		section := handler.trailerSection(c.Request.Context(), id)
		section.Message = message
		handler.renderer.Fragment(c, status, "trailer_section", section)
		return
	}
	section := handler.trailerSection(c.Request.Context(), id)
	section.Message = "Trailer uploaded."
	handler.renderer.Fragment(c, http.StatusOK, "trailer_section", section)
}

// DeleteTrailer removes the trailer and returns the refreshed section with
// 200 (not the empty body of a movie row delete), so the upload control shows.
func (handler *CatalogHandler) DeleteTrailer(c *gin.Context) {
	id, ok := handler.trailerMovieID(c)
	if !ok {
		return
	}
	err := handler.trailers.Delete(c.Request.Context(), id)
	var notFound service.NotFoundError
	if err != nil && !errors.As(err, &notFound) {
		status, message := http.StatusInternalServerError, "Unable to delete the trailer."
		if errors.Is(err, service.ErrStorageUnavailable) {
			status, message = http.StatusServiceUnavailable, "Trailer storage is unavailable; the trailer was kept."
		} else {
			log.Printf("delete web trailer: movie_id=%d: %v", id, err)
		}
		section := handler.trailerSection(c.Request.Context(), id)
		section.Message = message
		handler.renderer.Fragment(c, status, "trailer_section", section)
		return
	}
	section := handler.trailerSection(c.Request.Context(), id)
	section.Message = "Trailer deleted."
	handler.renderer.Fragment(c, http.StatusOK, "trailer_section", section)
}

// trailerMovieID parses the id and checks that the movie exists, answering
// HTMX requests with a bare status like the movie row delete.
func (handler *CatalogHandler) trailerMovieID(c *gin.Context) (uint32, bool) {
	id, err := service.ParseMovieID(c.Param("id"))
	if err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return 0, false
	}
	if err := handler.trailers.RequireMovie(c.Request.Context(), id); err != nil {
		var notFound service.NotFoundError
		if errors.As(err, &notFound) {
			c.AbortWithStatus(http.StatusNotFound)
			return 0, false
		}
		log.Printf("load web trailer movie: %v", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return 0, false
	}
	return id, true
}

// trailerSection describes the movie's trailer for staff. It checks storage,
// so a lost object is shown as unavailable.
func (handler *CatalogHandler) trailerSection(ctx context.Context, id uint32) TrailerSectionView {
	section := TrailerSectionView{MovieID: id, State: "none"}
	trailer, err := handler.trailers.Metadata(ctx, id)
	var unavailable service.TrailerUnavailableError
	var notFound service.NotFoundError
	switch {
	case err == nil:
		section.State = "ready"
		section.SizeText = formatSize(trailer.SizeBytes)
	case errors.As(err, &unavailable):
		section.State = "unavailable"
	case errors.As(err, &notFound):
	default:
		if !errors.Is(err, service.ErrStorageUnavailable) {
			log.Printf("load web trailer section: movie_id=%d: %v", id, err)
		}
		section.State = "error"
	}
	return section
}

func trailerUploadFailure(err error) (int, string) {
	var tooLarge service.PayloadTooLargeError
	var unsupported service.UnsupportedMediaTypeError
	var validation service.ValidationError
	var notFound service.NotFoundError
	switch {
	case errors.As(err, &notFound):
		// The movie was deleted during the upload.
		return http.StatusNotFound, notFound.Message
	case errors.As(err, &tooLarge):
		return http.StatusRequestEntityTooLarge, tooLarge.Error()
	case errors.As(err, &unsupported):
		return http.StatusUnsupportedMediaType, unsupported.Error()
	case errors.As(err, &validation):
		return http.StatusUnprocessableEntity, validation.Message
	case errors.Is(err, service.ErrStorageUnavailable):
		return http.StatusServiceUnavailable, "Trailer storage is unavailable. Please try again later."
	default:
		return http.StatusInternalServerError, "Unable to upload the trailer."
	}
}

func formatSize(bytes int64) string {
	const mebibyte = 1 << 20
	if bytes >= mebibyte {
		return fmt.Sprintf("%.1f MiB", float64(bytes)/mebibyte)
	}
	return fmt.Sprintf("%.1f KiB", float64(bytes)/1024)
}
