package worker

import (
	"reflect"
	"testing"

	"github.com/meanii/downly/internal/media"
)

func TestAlbumGroups(t *testing.T) {
	P, V, A, D, G := media.Photo, media.Video, media.Audio, media.Document, media.Animation
	tests := []struct {
		kinds []media.Kind
		want  [][]int
	}{
		{[]media.Kind{P, V, P}, [][]int{{0, 1, 2}}},
		{[]media.Kind{P, A, A, P}, [][]int{{0}, {1, 2}, {3}}},
		{[]media.Kind{D, D, P}, [][]int{{0, 1}, {2}}},
		{[]media.Kind{G, G}, [][]int{{0}, {1}}},
		{[]media.Kind{P, P, P, P, P, P, P, P, P, P, P, P}, [][]int{{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, {10, 11}}},
		{nil, nil},
	}
	for _, tt := range tests {
		if got := albumGroups(tt.kinds); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("albumGroups(%v) = %v, want %v", tt.kinds, got, tt.want)
		}
	}
}
