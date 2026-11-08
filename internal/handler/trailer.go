package handler

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"homework/internal/apperr"
	"homework/internal/middleware"
	"homework/internal/model"
	"homework/internal/service"
)

// multipartOverhead allows for the multipart boundaries and part headers
// around a file of exactly service.MaxTrailerSize.
const multipartOverhead = 64 << 10

// MaxTrailerRequestSize is the request body limit of the upload route.
const MaxTrailerRequestSize = service.MaxTrailerSize + multipartOverhead

type TrailerHandler struct {
	service *service.TrailerService
}

func NewTrailerHandler(trailerService *service.TrailerService) *TrailerHandler {
	return &TrailerHandler{service: trailerService}
}

func RegisterTrailerAPI(public, staff *gin.RouterGroup, trailers *TrailerHandler) {
	public.GET("/movies/:id/trailer", trailers.metadata)
	public.GET("/movies/:id/trailer/video", trailers.stream)

	staff.PUT("/movies/:id/trailer", trailers.upload)
}

// upload godoc
// @Summary Upload or replace a movie trailer
// @Description Stores one MP4 trailer (at most 50 MiB) for the movie. The file type is checked by its MP4 signature, not by the Content-Type header.
// @Tags trailers
// @Accept multipart/form-data
// @Produce json
// @Param id path int64 true "Movie ID" minimum(0) maximum(4294967295)
// @Param file formData file true "MP4 video"
// @Security BearerAuth
// @Success 200 {object} model.Trailer "Trailer replaced"
// @Success 201 {object} model.Trailer "Trailer created"
// @Failure 400 {object} model.ErrorResponse
// @Failure 401 {object} model.ErrorResponse
// @Failure 403 {object} model.ErrorResponse
// @Failure 404 {object} model.ErrorResponse
// @Failure 413 {object} model.ErrorResponse
// @Failure 415 {object} model.ErrorResponse
// @Failure 422 {object} model.ErrorResponse
// @Failure 503 {object} model.ErrorResponse
// @Router /movies/{id}/trailer [put]
func (handler *TrailerHandler) upload(c *gin.Context) {
	userID, ok := middleware.UserIDFrom(c)
	if !ok {
		apperr.WriteError(c, apperr.HTTPError{Status: http.StatusUnauthorized, Code: "unauthorized", Message: "Authentication required"})
		return
	}
	id, err := parseID(c)
	if err != nil {
		apperr.WriteError(c, err)
		return
	}
	if err := handler.service.RequireMovie(c.Request.Context(), id); err != nil {
		apperr.WriteError(c, err)
		return
	}
	if c.Request.ContentLength > MaxTrailerRequestSize {
		apperr.WriteError(c, service.PayloadTooLargeError{})
		return
	}

	body := &limitedBody{ReadCloser: http.MaxBytesReader(c.Writer, c.Request.Body, MaxTrailerRequestSize)}
	c.Request.Body = body
	file, err := trailerFilePart(c.Request)
	if err != nil {
		apperr.WriteError(c, uploadError(body, err))
		return
	}
	trailer, created, err := handler.service.Upload(c.Request.Context(), id, file, uint64(userID))
	if err != nil {
		apperr.WriteError(c, uploadError(body, err))
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.JSON(status, trailer)
}

// metadata godoc
// @Summary Get a movie trailer's metadata
// @Description Returns the trailer metadata after checking that the video still exists in storage.
// @Tags trailers
// @Produce json
// @Param id path int64 true "Movie ID" minimum(0) maximum(4294967295)
// @Success 200 {object} model.Trailer
// @Failure 400 {object} model.ErrorResponse
// @Failure 404 {object} model.ErrorResponse "not_found or trailer_unavailable"
// @Failure 503 {object} model.ErrorResponse
// @Router /movies/{id}/trailer [get]
func (handler *TrailerHandler) metadata(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		apperr.WriteError(c, err)
		return
	}
	trailer, err := handler.service.Metadata(c.Request.Context(), id)
	if err != nil {
		apperr.WriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, trailer)
}

// stream godoc
// @Summary Stream a movie trailer
// @Description Streams the MP4 video. A single "bytes=" Range returns 206; a multi-range or malformed Range returns the whole file.
// @Tags trailers
// @Produce video/mp4
// @Param id path int64 true "Movie ID" minimum(0) maximum(4294967295)
// @Param Range header string false "Single byte range, for example bytes=0-1023"
// @Success 200 {file} file
// @Success 206 {file} file
// @Failure 400 {object} model.ErrorResponse
// @Failure 404 {object} model.ErrorResponse "not_found or trailer_unavailable"
// @Failure 416 {object} model.ErrorResponse
// @Failure 503 {object} model.ErrorResponse
// @Router /movies/{id}/trailer/video [get]
func (handler *TrailerHandler) stream(c *gin.Context) {
	id, err := parseID(c)
	if err != nil {
		apperr.WriteError(c, err)
		return
	}
	object, err := handler.service.Open(c.Request.Context(), id, c.GetHeader("Range"))
	if err != nil {
		var rangeError service.RangeNotSatisfiableError
		if errors.As(err, &rangeError) {
			c.Header("Content-Range", fmt.Sprintf("bytes */%d", rangeError.Size))
		}
		apperr.WriteError(c, err)
		return
	}
	defer object.Body.Close()

	header := c.Writer.Header()
	header.Set("Content-Type", model.TrailerContentType)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Accept-Ranges", "bytes")
	header.Set("Content-Length", strconv.FormatInt(object.ContentLength, 10))
	status := http.StatusOK
	if object.ContentRange != "" {
		header.Set("Content-Range", object.ContentRange)
		status = http.StatusPartialContent
	}
	c.Status(status)
	// A copy error means the client went away; the status is already sent.
	_, _ = io.Copy(c.Writer, object.Body)
}

func trailerFilePart(request *http.Request) (*multipart.Part, error) {
	reader, err := request.MultipartReader()
	if err != nil {
		return nil, service.ValidationError{Message: "Request must be multipart/form-data with a file field"}
	}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, service.ValidationError{Message: "file field is required"}
		}
		if err != nil {
			return nil, service.ValidationError{Message: "Request body is not valid multipart/form-data"}
		}
		if part.FormName() == "file" {
			return part, nil
		}
	}
}

// uploadError reports 413 whenever the body limit was hit, whatever error
// the multipart parser or the service turned it into.
func uploadError(body *limitedBody, err error) error {
	if body.exceeded {
		return service.PayloadTooLargeError{}
	}
	return err
}

type limitedBody struct {
	io.ReadCloser
	exceeded bool
}

func (body *limitedBody) Read(p []byte) (int, error) {
	n, err := body.ReadCloser.Read(p)
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		body.exceeded = true
	}
	return n, err
}
