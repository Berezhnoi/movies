package app_test

import (
	"net/http"
	"testing"
)

// Stage 2: validation and error handling.

func TestStage2_MissingRequiredFieldReturns422(t *testing.T) {
	v := moviesVariant

	for _, field := range v.Required {
		field := field
		t.Run(field, func(t *testing.T) {
			e := newEnv(t)

			r := e.doJSON(http.MethodPost, e.base(), without(e.valid(), field))

			e.wantError(r, http.StatusUnprocessableEntity, "validation_error")
		})
	}
}

func TestStage2_EmptyRequiredFieldReturns422(t *testing.T) {
	v := moviesVariant

	for _, field := range v.Required {
		field := field
		t.Run(field, func(t *testing.T) {
			e := newEnv(t)

			r := e.doJSON(http.MethodPost, e.base(), with(e.valid(), field, ""))

			e.wantError(r, http.StatusUnprocessableEntity, "validation_error")
		})
	}
}

func TestStage2_MalformedJSONReturns422(t *testing.T) {
	bodies := map[string]string{
		"broken json": `{not json`,
		"empty body":  "",
		"json array":  `[1, 2, 3]`,
	}

	for name, body := range bodies {
		body := body
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)

			r := e.do(http.MethodPost, e.base(), body)

			e.wantError(r, http.StatusUnprocessableEntity, "validation_error")
		})
	}
}

func TestStage2_InvalidIDReturns400(t *testing.T) {
	for _, id := range []string{"abc", "-1", "1.5"} {
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
			id, method := id, method
			t.Run(method+"/"+id, func(t *testing.T) {
				e := newEnv(t)

				var r resp
				if method == http.MethodPut {
					r = e.doJSON(method, e.item(id), e.valid())
				} else {
					r = e.do(method, e.item(id), "")
				}

				e.wantError(r, http.StatusBadRequest, "invalid_id")
			})
		}
	}
}

func TestStage2_NotFoundReturns404(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		method := method
		t.Run(method, func(t *testing.T) {
			e := newEnv(t)

			var r resp
			if method == http.MethodPut {
				r = e.doJSON(method, e.item(999), e.valid())
			} else {
				r = e.do(method, e.item(999), "")
			}

			e.wantError(r, http.StatusNotFound, "not_found")
		})
	}
}

func TestStage2_PutChecksIDThenBodyThenExistence(t *testing.T) {
	e := newEnv(t)

	// 1. an invalid id wins over an invalid body
	r := e.do(http.MethodPut, e.item("abc"), `{not json`)
	e.wantError(r, http.StatusBadRequest, "invalid_id")

	// 2. an invalid body wins over a missing resource
	r = e.do(http.MethodPut, e.item(999), `{not json`)
	e.wantError(r, http.StatusUnprocessableEntity, "validation_error")

	// 3. a valid request for a missing resource is a 404
	r = e.doJSON(http.MethodPut, e.item(999), e.valid())
	e.wantError(r, http.StatusNotFound, "not_found")
}

func TestStage2_ErrorResponsesAreJSON(t *testing.T) {
	e := newEnv(t)

	responses := map[string]resp{
		"404": e.do(http.MethodGet, e.item(999), ""),
		"400": e.do(http.MethodGet, e.item("abc"), ""),
		"422": e.do(http.MethodPost, e.base(), `{}`),
	}

	for name, r := range responses {
		if !isJSONContent(r) {
			t.Errorf("%s response: Content-Type must start with application/json, got %q", name, r.Header.Get("Content-Type"))
		}
	}
}
