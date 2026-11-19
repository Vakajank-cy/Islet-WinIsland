module islet

go 1.24

require (
	github.com/fogleman/gg v0.0.0
	github.com/golang/freetype v0.0.0
	golang.org/x/image v0.20.0
)

replace (
	github.com/fogleman/gg => ./third_party/gg
	github.com/golang/freetype => ./third_party/freetype
	golang.org/x/image => ./third_party/image
)
