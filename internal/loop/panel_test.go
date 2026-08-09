package loop

import "testing"

func TestPanelWidth(t *testing.T) {
	cases := map[int]int{40: 0, 64: 32, 100: 32, 200: 44}
	for input, want := range cases {
		if got := PanelWidth(input); got != want {
			t.Errorf("PanelWidth(%d)=%d, want %d", input, got, want)
		}
	}
}
func TestResizeDirection(t *testing.T) {
	direction, amount, ok := ResizeDirection(30, 40)
	if !ok || direction != "right" || amount != 10 {
		t.Fatalf("got %q %d %v", direction, amount, ok)
	}
	if _, _, ok := ResizeDirection(40, 41); ok {
		t.Fatal("one column should not resize")
	}
}
