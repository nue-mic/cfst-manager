package store

import (
	"crypto/rand"
	"encoding/base64"
	"strings"
	"time"

	"github.com/nue-mic/cfst-manager/internal/engine"
)

// ---------- Settings ----------

// GetSettings 返回设置副本。
func (s *Store) GetSettings() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.meta.Settings
}

// UpdateSettings 覆盖设置并持久化。
func (s *Store) UpdateSettings(set Settings) error {
	if set.PublicResultMax <= 0 {
		set.PublicResultMax = 10
	}
	set.DefaultConfig.Normalize() // 补全默认测速参数，避免存入 0/空
	s.mu.Lock()
	defer s.mu.Unlock()
	s.meta.Settings = set
	return s.persistMetaLocked()
}

// ---------- Profiles ----------

// cloneProfile 深拷贝 Profile（含 LineV4/LineV6 两个 map）。
// 必须深拷贝：直接 `*p` 是浅拷贝，会让对外返回的副本与 store 内部共享同一 map 头，
// 一旦调用方在锁外读 map 而 DeleteRun 等在写锁内 delete 同一 map，
// 会触发 Go 运行时 "concurrent map read and map write" 致命崩溃。
func cloneProfile(p *Profile) Profile {
	cp := *p
	if p.LineV4 != nil {
		cp.LineV4 = make(map[string]string, len(p.LineV4))
		for k, v := range p.LineV4 {
			cp.LineV4[k] = v
		}
	}
	if p.LineV6 != nil {
		cp.LineV6 = make(map[string]string, len(p.LineV6))
		for k, v := range p.LineV6 {
			cp.LineV6[k] = v
		}
	}
	return cp
}

// ListProfiles 返回全部 Profile 的深拷贝。
func (s *Store) ListProfiles() []Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Profile, 0, len(s.meta.Profiles))
	for _, p := range s.meta.Profiles {
		out = append(out, cloneProfile(p))
	}
	return out
}

// GetProfile 返回指定 Profile 的深拷贝。
func (s *Store) GetProfile(name string) (Profile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.meta.Profiles[name]
	if !ok {
		return Profile{}, false
	}
	return cloneProfile(p), true
}

// UpsertProfile 新增/更新 Profile（深拷贝入参，避免与调用方共享 map）。
func (s *Store) UpsertProfile(p Profile) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := cloneProfile(&p)
	s.meta.Profiles[p.Name] = &cp
	return s.persistMetaLocked()
}

// Publish 把某 run 发布到 profile 的对应版本槽位。
func (s *Store) Publish(profile, ipVersion, runID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publishLocked(profile, ipVersion, runID)
	return s.persistMetaLocked()
}

func (s *Store) publishLocked(profile, ipVersion, runID string) {
	p, ok := s.meta.Profiles[profile]
	if !ok {
		p = &Profile{Name: profile, Title: profile}
		s.meta.Profiles[profile] = p
	}
	switch ipVersion {
	case "v6":
		p.PublishedV6RunID = runID
	case "v4":
		p.PublishedV4RunID = runID
	case "mixed":
		p.PublishedV4RunID = runID
		p.PublishedV6RunID = runID
	}
}

// ---------- 公开 API 取数 ----------

// GetPublishedLines 返回某 profile 在指定版本(v4/v6)下三网(CM/CU/CT)各自的优选结果。
// 每条线路优先用 Profile 的线路覆盖，否则回退到该版本已发布的 run。结果按版本过滤。
func (s *Store) GetPublishedLines(profile, ipVersion string) map[string][]engine.Result {
	s.mu.RLock()
	p, ok := s.meta.Profiles[profile]
	var pCopy Profile
	if ok {
		pCopy = cloneProfile(p) // 深拷贝：随后在锁外读 LineV4/LineV6，绝不能与内部共享 map
	}
	s.mu.RUnlock()

	out := map[string][]engine.Result{LineCM: {}, LineCU: {}, LineCT: {}}
	if !ok {
		return out
	}

	defaultRun := pCopy.PublishedV4RunID
	lineMap := pCopy.LineV4
	if ipVersion == "v6" {
		defaultRun = pCopy.PublishedV6RunID
		lineMap = pCopy.LineV6
	}

	cache := map[string][]engine.Result{}
	resolve := func(runID string) []engine.Result {
		if runID == "" {
			return nil
		}
		if v, hit := cache[runID]; hit {
			return v
		}
		run, err := s.GetRun(runID)
		var res []engine.Result
		if err == nil {
			res = filterByVersion(run.Results, ipVersion)
		}
		cache[runID] = res
		return res
	}

	for _, line := range []string{LineCM, LineCU, LineCT} {
		runID := defaultRun
		if lineMap != nil {
			if v, has := lineMap[line]; has && v != "" {
				runID = v
			}
		}
		if r := resolve(runID); r != nil {
			out[line] = r
		}
	}
	return out
}

func filterByVersion(results []engine.Result, ipVersion string) []engine.Result {
	out := make([]engine.Result, 0, len(results))
	for _, r := range results {
		isV6 := strings.Contains(r.IP, ":")
		if ipVersion == "v6" && !isV6 {
			continue
		}
		if ipVersion == "v4" && isV6 {
			continue
		}
		out = append(out, r)
	}
	return out
}

// ---------- Licenses ----------

// ListLicenses 返回全部授权密钥副本。
func (s *Store) ListLicenses() []License {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]License, len(s.meta.Licenses))
	copy(out, s.meta.Licenses)
	return out
}

// AddLicense 新增一个授权密钥；key 为空时自动生成。
func (s *Store) AddLicense(key, note string, count int64) (License, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(key) == "" {
		key = genLicenseKey()
	}
	if count <= 0 {
		count = 99999999
	}
	lic := License{Key: key, Note: note, Enabled: true, Count: count, CreatedAt: time.Now().UTC()}
	s.meta.Licenses = append(s.meta.Licenses, lic)
	if err := s.persistMetaLocked(); err != nil {
		return License{}, err
	}
	return lic, nil
}

// UpdateLicense 更新指定 key 的备注/启用状态/余额。
func (s *Store) UpdateLicense(key, note string, enabled bool, count int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.meta.Licenses {
		if s.meta.Licenses[i].Key == key {
			s.meta.Licenses[i].Note = note
			s.meta.Licenses[i].Enabled = enabled
			if count > 0 {
				s.meta.Licenses[i].Count = count
			}
			return s.persistMetaLocked()
		}
	}
	return ErrNotFound
}

// DeleteLicense 删除指定 key。
func (s *Store) DeleteLicense(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.meta.Licenses[:0]
	found := false
	for _, l := range s.meta.Licenses {
		if l.Key == key {
			found = true
			continue
		}
		out = append(out, l)
	}
	s.meta.Licenses = out
	if !found {
		return ErrNotFound
	}
	return s.persistMetaLocked()
}

// ValidateLicense 校验对外 API 的 key：开放模式下任意非空 key 通过；
// 否则必须命中一个已启用的密钥。返回是否有效及匹配到的授权(开放模式返回零值 License)。
func (s *Store) ValidateLicense(key string) (License, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.meta.Settings.PublicOpen {
		return License{Key: key, Count: 99999999}, true
	}
	for _, l := range s.meta.Licenses {
		if l.Enabled && l.Key == key {
			return l, true
		}
	}
	return License{}, false
}

func genLicenseKey() string {
	buf := make([]byte, 9)
	_, _ = rand.Read(buf)
	return strings.TrimRight(base64.URLEncoding.EncodeToString(buf), "=")
}
