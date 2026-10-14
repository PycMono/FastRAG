package dto

import "testing"

// 空 SplitOptions 必须补成默认值，而不是留零值下去（零值会被 Normalize 当默认，
// 但这里要钉住的是「ChunkOptions 确实转到了 Normalize」这条路径）。
func TestDocParseDTO_ChunkOptions_Defaults(t *testing.T) {
	in := &DocParseDTO{}

	opts, err := in.ChunkOptions()
	if err != nil {
		t.Fatal(err)
	}
	if opts.ChunkSize != 800 || opts.SplitLevel != 2 || opts.MinChunk != 50 {
		t.Errorf("应补成默认切片参数 800/2/50，得到 %d/%d/%d",
			opts.ChunkSize, opts.SplitLevel, opts.MinChunk)
	}
}

// 请求给了就用请求的。
func TestDocParseDTO_ChunkOptions_Overridden(t *testing.T) {
	in := &DocParseDTO{SplitOptions: &SplitOptionsDTO{ChunkSize: 1200, SplitLevel: 3, MinChunk: 100}}

	opts, err := in.ChunkOptions()
	if err != nil {
		t.Fatal(err)
	}
	if opts.ChunkSize != 1200 || opts.SplitLevel != 3 || opts.MinChunk != 100 {
		t.Errorf("应原样采用请求给的参数，得到 %d/%d/%d",
			opts.ChunkSize, opts.SplitLevel, opts.MinChunk)
	}
}

// 越界值必须在 Normalize 那一层被拒，不能一路带到切片器里。
func TestDocParseDTO_ChunkOptions_OutOfRange(t *testing.T) {
	in := &DocParseDTO{SplitOptions: &SplitOptionsDTO{ChunkSize: 99999}}

	if _, err := in.ChunkOptions(); err == nil {
		t.Fatal("chunk_size 越界必须报参数错误")
	}
}

// URL 的 scheme 只有 http/https 放行：binding 的 `url` tag 拦不住 ftp://，
// 这里补齐（§5、§9）。
func TestDocParseDTO_Validate_Scheme(t *testing.T) {
	for _, u := range []string{"http://a.com/x.pdf", "https://a.com/x.pdf", "HTTPS://a.com/x"} {
		if err := (&DocParseDTO{URL: u}).Validate(); err != nil {
			t.Errorf("%q 应放行，得到 %v", u, err)
		}
	}
	for _, u := range []string{"ftp://a.com/x", "file:///etc/passwd", "", "://a.com", "a.com/x.pdf"} {
		if err := (&DocParseDTO{URL: u}).Validate(); err == nil {
			t.Errorf("%q 应拒绝", u)
		}
	}
}
