package scene

import "testing"

func TestFloor(t *testing.T) {
	cases := []struct {
		r    Reading
		want int
	}{
		{Reading{Flow: "debris_flow"}, 3},
		{Reading{Flow: "muddy_water", Structures: "at_risk"}, 3}, // Kedarnath: "muddy", houses hit
		{Reading{Flow: "not_visible", Structures: "damaged_or_destroyed"}, 3},
		{Reading{Flow: "muddy_water", DebrisKinds: []string{"building_materials"}}, 2},
		{Reading{Flow: "muddy_water", Surface: "turbulent", DebrisKinds: []string{"logs_or_whole_trees"}}, 2},
		{Reading{Flow: "muddy_water", Surface: "rippled", DebrisKinds: []string{"branches"}}, 0},
		{Reading{Flow: "clear_water", Structures: "intact"}, 0},
	}
	for _, c := range cases {
		if got, _ := c.r.Floor(); got != c.want {
			t.Errorf("%+v: got %d, want %d", c.r, got, c.want)
		}
	}
}

func TestChangeFloor(t *testing.T) {
	cases := []struct {
		c    Change
		want int
	}{
		{Change{StructuresChanged: "missing"}, 3},
		{Change{ChannelBlocked: "partly"}, 2},
		{Change{Danger: "significant", NewMaterial: []string{"logs_or_whole_trees"}}, 2},
		{Change{Danger: "significant", WaterChange: "higher"}, 0}, // the level rules handle plain rises
		{Change{Danger: "none", ChannelBlocked: "no", StructuresChanged: "unchanged"}, 0},
	}
	for _, c := range cases {
		if got, _ := c.c.Floor(); got != c.want {
			t.Errorf("%+v: got %d, want %d", c.c, got, c.want)
		}
	}
}
