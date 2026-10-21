package render

type Preset struct{ Name, Summary, Example string }

func Render(src []byte) ([]byte, error) { return src, nil }

func RenderBlock(src []byte, id string) ([]byte, error) { return nil, nil }

func CSS() []byte { return nil }

func Presets() []Preset { return nil }
