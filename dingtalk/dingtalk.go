// 钉钉连接器：五面与飞书同构 = token / 空间与文档 / 正文 / 权限成员 /
// 通讯录（部门树）。端点形状按公开文档钉死（真租户校准点挂仓 issue，单点改本文件）；
// 响应无统一信封（新 v1.0 风格直接 JSON，旧 topapi 风格 {errcode, result}——
// 两种都归一进本实现，对上零泄漏）。
package dingtalk

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/tunsuy/agthelm-connectors/contract"
)

// 钉钉 API 面板（端点单点纪律：平台端点字符串只准出现在本包）。
const (
	dingtalkTokenPath   = "/v1.0/oauth2/accessToken"
	dingtalkSpacesPath  = "/v1.0/storage/spaces"
	dingtalkDentriesFmt = "/v1.0/storage/spaces/%s/dentries"
	dingtalkContentFmt  = "/v1.0/doc/documents/%s/rawContent"
	dingtalkPermFmt     = "/v1.0/drive/spaces/%s/dentries/%s/permissions"
	dingtalkDeptPath    = "/topapi/v2/department/listsub"
	dingtalkUserPath    = "/topapi/v2/user/list"

	dingtalkContentType = "dingtalk-raw-content"
	dingtalkPlatform    = "dingtalk"
)

// DingTalkConfig 钉钉连接器配置（BaseURL 可注入 = fake 剑本消费同一实现）。
type DingTalkConfig struct {
	BaseURL     string // prod = 钉钉开放平台根（配置面承载；本包源码零域名）
	AppKey      string // 应用凭据（live 验证挂仓 issue）
	AppSecret   string
	Spaces      []string // 空间 id 白名单（空 = 全部发现的空间）
	MaxDocBytes int64
}

// dingtalkClient 钉钉 REST 客户端（accessToken 缓存 + 过期自动刷新重试一次）。
type dingtalkClient struct {
	cfg    DingTalkConfig
	hc     *http.Client
	token  string
	expiry time.Time
}

// NewDingTalk 构造钉钉连接器（cfg 校验失败 → error，接线判定归调用方告警降级）。
func NewDingTalk(cfg DingTalkConfig) (contract.Connector, error) {
	if cfg.BaseURL == "" || cfg.AppKey == "" || cfg.AppSecret == "" {
		return nil, fmt.Errorf("dingtalk: 配置不完整（BaseURL/AppKey/AppSecret）")
	}
	if cfg.MaxDocBytes <= 0 {
		cfg.MaxDocBytes = contract.DefaultMaxDocBytes
	}
	return &dingtalkClient{cfg: cfg, hc: &http.Client{Timeout: 30 * time.Second}}, nil
}

func (c *dingtalkClient) Platform() string { return dingtalkPlatform }

// call 统一出口：v1.0 风格直接解 out；topapi 风格 errcode != 0 → error。
// 401/token 失效判定（v1.0 返回 401；topapi errcode 88 = token 失效）→ 刷新一次重试。
func (c *dingtalkClient) call(ctx context.Context, method, path string, query url.Values, body, out any) error {
	if err := c.ensureToken(ctx); err != nil {
		return err
	}
	err := c.do(ctx, method, path, query, body, out, c.token)
	if err == nil {
		return nil
	}
	if isTokenStale(err) {
		c.token, c.expiry = "", time.Time{}
		if err2 := c.ensureToken(ctx); err2 != nil {
			return err2
		}
		return c.do(ctx, method, path, query, body, out, c.token)
	}
	return err
}

// isTokenStale 判定响应错误是否 token 失效面（路径前缀带口勿细判别）。
func isTokenStale(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return bytes.Contains([]byte(msg), []byte("401")) ||
		bytes.Contains([]byte(msg), []byte("errcode=88"))
}

// do 发请求（v1.0 语义：非 200 → error）。token 参数化 = 操作面（actions_exec.go）
// 可复用：同步面传应用 accessToken，操作面 OnBehalf 时传发起人个人令牌（同一
// x-acs-dingtalk-access-token 头——真租户校准点挂仓 issue）。
func (c *dingtalkClient) do(ctx context.Context, method, path string, query url.Values, body, out any, token string) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	u := c.cfg.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("x-acs-dingtalk-access-token", token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("dingtalk 传输 %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, contract.MaxResponseBytes))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("dingtalk %s: http 401（token 失效）", path)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("dingtalk %s: http %d", path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	// topapi 风格：errcode != 0 → 业务失败（v1.0 无 errcode 字段，反序列化容忍）
	var probe struct {
		ErrCode *int `json:"errcode"`
	}
	if err := json.Unmarshal(raw, &probe); err == nil && probe.ErrCode != nil && *probe.ErrCode != 0 {
		return fmt.Errorf("dingtalk %s: errcode=%d", path, *probe.ErrCode)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("dingtalk %s: 响应解析: %w", path, err)
	}
	return nil
}

// ensureToken 取/缓存 accessToken（expire 提前 60s 视为过期；token 端点自带凭据无鉴权头）。
func (c *dingtalkClient) ensureToken(ctx context.Context) error {
	if c.token != "" && time.Now().Before(c.expiry.Add(-60*time.Second)) {
		return nil
	}
	var out struct {
		AccessToken string `json:"accessToken"`
		ExpireIn    int    `json:"expireIn"`
	}
	saved := c.token
	c.token = ""
	err := c.do(ctx, http.MethodPost, dingtalkTokenPath, nil,
		map[string]string{"appKey": c.cfg.AppKey, "appSecret": c.cfg.AppSecret}, &out, c.token)
	if err != nil {
		c.token = saved
		return fmt.Errorf("dingtalk token: %w", err)
	}
	if out.AccessToken == "" {
		return fmt.Errorf("dingtalk token: 空口令")
	}
	c.token, c.expiry = out.AccessToken, time.Now().Add(time.Duration(out.ExpireIn)*time.Second)
	return nil
}

// ---------- ① 文档枚举 ----------

// LoadDocs 全量拉取（首轮）。
func (c *dingtalkClient) LoadDocs(ctx context.Context) ([]contract.Document, error) {
	return c.listDocs(ctx)
}

// PollDocs 增量轮询：重列全集（缺席 = 删除），cursor = 本轮最大 updatedAt。
func (c *dingtalkClient) PollDocs(ctx context.Context, _ string) ([]contract.Document, string, error) {
	docs, err := c.listDocs(ctx)
	if err != nil {
		return nil, "", err
	}
	cursor := ""
	for _, d := range docs {
		if s := d.SourceUpdatedAt.Format(time.RFC3339); s > cursor {
			cursor = s
		}
	}
	return docs, cursor, nil
}

type dingtalkSpace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type dingtalkDentry struct {
	ID        string `json:"id"` // dentry id = 文档 id
	Name      string `json:"name"`
	Type      string `json:"type"`      // file|folder|doc
	UpdatedAt string `json:"updatedAt"` // unix 毫秒字符串
	CreatorID string `json:"creatorId"`
}

// listDocs 空间列表（分页）→ 每空间 dentries（分页），type=doc 拉正文。
func (c *dingtalkClient) listDocs(ctx context.Context) ([]contract.Document, error) {
	var spaces struct {
		Spaces    []dingtalkSpace `json:"spaces"`
		NextToken string          `json:"nextToken"`
	}
	var spaceIDs []dingtalkSpace
	next := ""
	for {
		q := url.Values{"maxResults": {"50"}}
		if next != "" {
			q.Set("nextToken", next)
		}
		if err := c.call(ctx, http.MethodGet, dingtalkSpacesPath, q, nil, &spaces); err != nil {
			return nil, err
		}
		for _, sp := range spaces.Spaces {
			if c.spaceAllowed(sp.ID) {
				spaceIDs = append(spaceIDs, sp)
			}
		}
		if spaces.NextToken == "" {
			break
		}
		next = spaces.NextToken
	}
	var docs []contract.Document
	for _, sp := range spaceIDs {
		if err := c.listDentries(ctx, sp.ID, &docs); err != nil {
			return nil, err
		}
	}
	return docs, nil
}

func (c *dingtalkClient) spaceAllowed(id string) bool {
	if len(c.cfg.Spaces) == 0 {
		return true
	}
	for _, s := range c.cfg.Spaces {
		if s == id {
			return true
		}
	}
	return false
}

// listDentries 分页枚举空间文档（parentEntryId 空 = 根层；folder 下钻一层不做——
// 真租户校准点挂仓 issue：空间内子目录树遍历按实测 API 补）。
func (c *dingtalkClient) listDentries(ctx context.Context, spaceID string, docs *[]contract.Document) error {
	next := ""
	for {
		q := url.Values{"maxResults": {"50"}}
		if next != "" {
			q.Set("nextToken", next)
		}
		var out struct {
			Dentries  []dingtalkDentry `json:"dentries"`
			NextToken string           `json:"nextToken"`
		}
		if err := c.call(ctx, http.MethodGet, fmt.Sprintf(dingtalkDentriesFmt, spaceID), q, nil, &out); err != nil {
			return err
		}
		for _, de := range out.Dentries {
			if de.Type != "doc" {
				continue
			}
			d, err := c.fetchDoc(ctx, de, spaceID)
			if err != nil {
				return err
			}
			*docs = append(*docs, d)
		}
		if out.NextToken == "" {
			return nil
		}
		next = out.NextToken
	}
}

// fetchDoc 拉单文档正文（护栏：超 MaxDocBytes 只保 metadata）。
func (c *dingtalkClient) fetchDoc(ctx context.Context, de dingtalkDentry, spaceID string) (contract.Document, error) {
	mod, _ := strconv.ParseInt(de.UpdatedAt, 10, 64)
	d := contract.Document{
		Platform:        dingtalkPlatform,
		ExternalID:      de.ID,
		SpaceID:         spaceID,
		Title:           de.Name,
		OwnerID:         de.CreatorID,
		ContentFormat:   dingtalkContentType,
		SourceUpdatedAt: time.Unix(mod/1000, 0).UTC(), // 毫秒 → 秒
	}
	var out struct {
		Content string `json:"content"`
	}
	if err := c.call(ctx, http.MethodGet, fmt.Sprintf(dingtalkContentFmt, de.ID), nil, nil, &out); err != nil {
		return d, err
	}
	raw := []byte(out.Content)
	d.SizeBytes = int64(len(raw))
	if int64(len(raw)) > c.cfg.MaxDocBytes {
		d.Oversize = true
		return d, nil
	}
	d.Raw = raw
	sum := sha256.Sum256(raw)
	d.ContentHash = hex.EncodeToString(sum[:])
	return d, nil
}

// ---------- ② 权限成员 ----------

// LoadACLs 全量权限元数据：枚举文档 → 逐 dentry 权限成员（单页契约，无分页参数——
// 真租户校准点挂仓 issue）。
func (c *dingtalkClient) LoadACLs(ctx context.Context) ([]contract.DocACL, error) {
	docs, err := c.listDocs(ctx)
	if err != nil {
		return nil, err
	}
	var acls []contract.DocACL
	for _, d := range docs {
		if d.Oversize {
			continue
		}
		var out struct {
			Members []dingtalkPermMember `json:"members"`
		}
		if err := c.call(ctx, http.MethodGet,
			fmt.Sprintf(dingtalkPermFmt, d.SpaceID, d.ExternalID), nil, nil, &out); err != nil {
			return nil, err
		}
		for _, m := range out.Members {
			acls = append(acls, contract.DocACL{
				Platform:   dingtalkPlatform,
				DocRef:     d.ExternalID,
				MemberType: m.MemberType,
				MemberID:   m.MemberID,
				Perm:       m.perm(),
			})
		}
	}
	return acls, nil
}

// dingtalkPermMember 权限成员行（memberType = user|department；role reader|editor）。
type dingtalkPermMember struct {
	MemberType string `json:"memberType"`
	MemberID   string `json:"memberId"`
	Role       string `json:"role"`
}

func (m dingtalkPermMember) perm() string {
	if m.Role == "editor" {
		return "write"
	}
	return "read"
}

// ---------- ③ 通讯录 ----------

// LoadDirectory 全量通讯录：部门树（listsub 递归）+ 逐部门用户分页（cursor/offset）。
func (c *dingtalkClient) LoadDirectory(ctx context.Context) ([]contract.IMContact, []contract.SourceDept, error) {
	var depts []contract.SourceDept
	if err := c.listDepts(ctx, 1, &depts); err != nil { // 根部门 = 1
		return nil, nil, err
	}
	seen := map[string]bool{}
	var contacts []contract.IMContact
	deptIDs := []int{1}
	for _, d := range depts {
		id, _ := strconv.Atoi(d.SourceDeptID)
		deptIDs = append(deptIDs, id)
	}
	for _, deptID := range deptIDs {
		if err := c.listUsers(ctx, deptID, seen, &contacts); err != nil {
			return nil, nil, err
		}
	}
	return contacts, depts, nil
}

// listDepts 递归枚举子部门（listsub 单层；walk 下钻）。
func (c *dingtalkClient) listDepts(ctx context.Context, deptID int, out *[]contract.SourceDept) error {
	var page struct {
		Result []dingtalkDept `json:"result"`
	}
	if err := c.call(ctx, http.MethodPost, dingtalkDeptPath, nil,
		map[string]any{"deptId": deptID}, &page); err != nil {
		return err
	}
	for _, d := range page.Result {
		*out = append(*out, contract.SourceDept{
			Platform:       dingtalkPlatform,
			SourceDeptID:   strconv.FormatInt(d.DeptID, 10),
			Name:           d.Name,
			ParentSourceID: strconv.Itoa(deptID),
		})
		if err := c.listDepts(ctx, int(d.DeptID), out); err != nil {
			return err
		}
	}
	return nil
}

type dingtalkDept struct {
	DeptID int64  `json:"dept_id"`
	Name   string `json:"name"`
}

// listUsers 枚举部门用户（cursor 分页 + seen 去重：多部门归属用户只计一次）。
func (c *dingtalkClient) listUsers(ctx context.Context, deptID int, seen map[string]bool, out *[]contract.IMContact) error {
	cursor := 0
	for {
		var page struct {
			Result struct {
				HasMore bool             `json:"has_more"`
				List    []dingtalkUserV2 `json:"list"`
			} `json:"result"`
		}
		if err := c.call(ctx, http.MethodPost, dingtalkUserPath, nil,
			map[string]any{"deptId": deptID, "cursor": cursor, "size": contract.DefaultPageSize}, &page); err != nil {
			return err
		}
		for _, u := range page.Result.List {
			if seen[u.UserID] {
				continue
			}
			seen[u.UserID] = true
			*out = append(*out, contract.IMContact{
				Platform: dingtalkPlatform,
				OpenID:   u.UserID, // 钉钉企业内 userid = 宿主侧绑定键
				UnionID:  u.UnionID,
				Email:    u.Email,
				Mobile:   u.Mobile,
				Name:     u.Name,
			})
		}
		if !page.Result.HasMore {
			return nil
		}
		cursor += contract.DefaultPageSize
	}
}

type dingtalkUserV2 struct {
	UserID  string `json:"userid"`
	UnionID string `json:"unionid"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Mobile  string `json:"mobile"`
}
