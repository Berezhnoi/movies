package web

import (
	"fmt"
	"time"

	"homework/internal/model"
)

type UserView struct {
	Name        string
	RoleLabel   string
	CanAddMovie bool
	CanRegister bool
}

type BasePage struct {
	Title     string
	User      *UserView
	CSRFToken string
	Flash     string
}

type CatalogPage struct {
	BasePage
	Movies  []MovieRowView
	Q       string
	Genre   string
	Genres  []GenreOption
	Page    int
	Limit   int
	HasPrev bool
	HasNext bool
	PrevURL string
	NextURL string
}

type GenreOption struct {
	Value    string
	Label    string
	Selected bool
}

type MovieRowView struct {
	ID          uint32
	Title       string
	Director    string
	Country     string
	Genre       string
	YearText    string
	Synopsis    string
	StatusLabel string
	StatusClass string
	CanEdit     bool
	CanDelete   bool
}

type ErrorPage struct {
	BasePage
	StatusText string
	Message    string
}

type LoginPage struct {
	BasePage
	Email  string
	Next   string
	Error  string
	Notice string
}

type RegisterPage struct {
	BasePage
	Email string
	Name  string
	Error string
}

type MovieFormPage struct {
	BasePage
	Heading     string
	Action      string
	SubmitText  string
	TitleValue  string
	Director    string
	Country     string
	Genre       string
	ReleaseYear string
	Synopsis    string
	Genres      []string
	Errors      []string
}

type ChatPage struct {
	BasePage
	MovieID    uint32
	MovieTitle string
}

func MapMovie(movie model.Movie, now time.Time) MovieRowView {
	row := MovieRowView{
		ID:       movie.ID,
		Title:    movie.Title,
		Director: movie.Director,
		Country:  movie.Country,
		Genre:    movie.Genre,
	}
	if movie.ReleaseYear != nil {
		row.YearText = fmt.Sprintf("%d", *movie.ReleaseYear)
		row.StatusLabel, row.StatusClass = ReleaseStatus(*movie.ReleaseYear, now)
	} else {
		row.YearText = "-"
	}
	if movie.Synopsis != nil {
		row.Synopsis = *movie.Synopsis
	}
	return row
}

func ReleaseStatus(year int, now time.Time) (string, string) {
	currentYear := now.Year()
	switch {
	case year > currentYear:
		return "Upcoming", "status-upcoming"
	case year >= currentYear-2:
		return "New", "status-new"
	default:
		return "Classic", "status-classic"
	}
}
