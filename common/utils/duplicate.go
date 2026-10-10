package utils

// DuplicatedKeys 找出 items 里 key 重复出现过的那些 key。
//
// key 由调用方给：什么算"同一个"是业务定的（这里是一组字段，别处可能只是
// 一个 id），通用函数只负责计数，不替业务定义同一性。
//
// 返回 key 集合而不是重复的条目：调用方通常要拿每个条目的 key 回头查表，
// 集合查询是 O(1)，且天然去重。
func DuplicatedKeys[T any, K comparable](items []T, key func(T) K) map[K]struct{} {
	count := make(map[K]int, len(items))
	for _, in := range items {
		count[key(in)]++
	}

	dup := make(map[K]struct{})
	for k, n := range count {
		if n > 1 {
			dup[k] = struct{}{}
		}
	}
	return dup
}
