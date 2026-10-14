package vo

import (
	"encoding/json"
	"testing"
)

// realSnowflake 是一个真实量级的雪花 ID（19 位）。
//
// 它在 JS 里存不住：`JSON.parse` 得到的是 2109838273063948300，比真值大 12。
// 页面拿着这个被舍入的数回来查 chunks / detail，就是 10501 document not found。
// 所以 doc_id 必须以**字符串**出现在 JSON 里——这个测试拿真值把每个带
// doc_id 的 VO 都过一遍，谁掉了 `,string` 就红。
const realSnowflake uint64 = 2109838273063948288

func TestDocIDIsSerializedAsString(t *testing.T) {
	cases := []struct {
		name string
		val  any
	}{
		{"SearchItemVO", SearchItemVO{DocID: realSnowflake}},
		{"DocIngestVO", DocIngestVO{DocID: realSnowflake}},
		{"DocListItemVO", DocListItemVO{DocID: realSnowflake}},
		{"DocDetailVO", DocDetailVO{DocID: realSnowflake}},
	}

	for _, c := range cases {
		b, err := json.Marshal(c.val)
		if err != nil {
			t.Fatalf("%s: marshal: %v", c.name, err)
		}

		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%s: unmarshal: %v", c.name, err)
		}

		// 先看类型：是数字就说明 tag 少了 ,string，浏览器那边一定会被改数。
		got, ok := m["doc_id"].(string)
		if !ok {
			t.Fatalf("%s: doc_id = %v (%T)，必须是 JSON 字符串", c.name, m["doc_id"], m["doc_id"])
		}
		if got != "2109838273063948288" {
			t.Fatalf("%s: doc_id = %q, want %q", c.name, got, "2109838273063948288")
		}
	}
}
