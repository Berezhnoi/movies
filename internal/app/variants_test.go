package app_test

// Variant describes the API resource under test: four required string fields,
// one optional integer and one optional text field.
// The filter field is always the fourth required field.
type Variant struct {
	Name     string
	Resource string
	Required []string
	OptInt   string
	OptText  string
}

// Filter returns the name of the field used both as a JSON field and as the list filter.
func (v Variant) Filter() string {
	return v.Required[3]
}

var moviesVariant = Variant{Name: "movies", Resource: "movies", Required: []string{"title", "director", "country", "genre"}, OptInt: "release_year", OptText: "synopsis"}
