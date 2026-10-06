// 飞书连接器：云文档（drive folder）+ 知识库（wiki space）两来源，
// 五面 = token / 文档列表 / 正文 / 权限成员 / 通讯录（含部门树）。
// 端点形状按公开文档钉死（真租户校准点挂仓 issue；单点改本文件）；
// fake 剑本（feishu_test.go）与本实现共享同一形状契约。
package feishu

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

// 飞书 API 面板（端点单点纪律：平台端点字符串只准出现在本包）。
const (
	feishuTokenPath   = "/open-apis/auth/v3/tenant_access_token/internal"
	feishuFilesPath   = "/open-apis/drive/v1/files"
	feishuContentPath = "/open-apis/docx/v1/documents/%s/raw_content"
	feishuPermPath    = "/open-apis/drive/v1/permissions/%s/members"
	feishuUsersPath   = "/open-apis/contact/v3/users/find_by_department"
	feishuDeptsPath   = "/open-apis/contact/v3/departments/children"
	feishuWikiSpaces  = "/open-apis/wiki/v2/spaces"
	feishuWikiNodes   = "/open-apis/wiki/v2/nodes"

	feishuContentType = "feishu-raw-content"
	feishuPlatform    = "feishu"
)

// FeishuConfig 飞书连接器配置（BaseURL 可注入 = fake 剑本消费同一实现）。
type FeishuConfig struct {
	BaseURL     string // prod = 飞书开放平台根（配置面承载；本包源码零域名）
	AppID       string // 应用凭据（自建应用；live 验证挂仓 issue）
	AppSecret   string
	Spaces      []string // 允许拉取的空间/目录 token 白名单（空 = 全部发现的空间）
	MaxDocBytes int64    // 单文档正文护栏（超限只保 metadata，runs 计数 oversize）
}

// feishuClient 飞书 REST 客户端（tenant_access_token 缓存 + 过期自动刷新重试一次）。
type feishuClient struct {
	cfg    FeishuConfig
	hc     *http.Client
	token  string
	expiry time.Time
}

// NewFeishu 构造飞书连接器（cfg 校验失败 → error，接线判定归调用方告警降级）。
func NewFeishu(cfg FeishuConfig) (contract.Connector, error) {
	if cfg.BaseURL == "" || cfg.AppID == "" || cfg.AppSecret == "" {
		return nil, fmt.Errorf("feishu: 配置不完整（BaseURL/AppID/AppSecret）")
	}
	if cfg.MaxDocBytes <= 0 {
		cfg.MaxDocBytes = contract.DefaultMaxDocBytes
	}
	return &feishuClient{cfg: cfg, hc: &http.Client{Timeout: 30 * time.Second}}, nil
}

func (c *feishuClient) Platform() string { return feishuPlatform }

// beeo 信封：飞书 REST 统一响应包 {code, msg, data}（code != 0 = 业务失败）。
type beeoEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// call 发起一次鉴权 GET（token 缺席/过期 → 先取；响应 code 99991663/99991661
// = token 失效 → 刷新一次重试）。
func (c *feishuClient) call(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	if err := c.ensureToken(ctx); err != nil {
		return err
	}
	resp, err := c.do(ctx, method, path, query, body, out, c.token)
	if err != nil {
		return err
	}
	if resp != nil && (resp.Code == 99991663 || resp.Code == 99991661) { // token 失效：刷新一次重试
		c.token, c.expiry = "", time.Time{}
		if err := c.ensureToken(ctx); err != nil {
			return err
		}
		resp, err = c.do(ctx, method, path, query, body, out, c.token)
		if err != nil {
			return err
		}
	}
	if resp != nil && resp.Code != 0 {
		return fmt.Errorf("feishu %s: code=%d msg=%s", path, resp.Code, resp.Msg)
	}
	return nil
}

// do 发请求 + 解 beeo 信封（transport 失败 → 原始错误；信封 Data 解进 out）。
// bearer 参数化 = 操作面（actions_exec.go）可复用：同步面传应用 token，操作
// 面 OnBehalf 时传发起人用户令牌。
func (c *feishuClient) do(ctx context.Context, method, path string, query url.Values, body any, out any, bearer string) (*beeoEnvelope, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	u := c.cfg.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	hresp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("feishu 传输 %s: %w", path, err)
	}
	defer func() { _ = hresp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(hresp.Body, contract.MaxResponseBytes))
	if err != nil {
		return nil, err
	}
	if hresp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("feishu %s: http %d", path, hresp.StatusCode)
	}
	var env beeoEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("feishu %s: 信封解析: %w", path, err)
	}
	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return nil, fmt.Errorf("feishu %s: data 解析: %w", path, err)
		}
	}
	return &env, nil
}

// ensureToken 取/缓存 tenant_access_token（expire 提前 60s 视为过期）。
func (c *feishuClient) ensureToken(ctx context.Context) error {
	if c.token != "" && time.Now().Before(c.expiry.Add(-60*time.Second)) {
		return nil
	}
	var out struct {
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"` // 秒
	}
	// token 端点本身不带 Bearer（自取）——此刻 c.token 已清空，同形传入
	saved := c.token
	c.token = ""
	resp, err := c.do(ctx, http.MethodPost, feishuTokenPath, nil,
		map[string]string{"app_id": c.cfg.AppID, "app_secret": c.cfg.AppSecret}, &out, c.token)
	if err != nil {
		c.token = saved
		return fmt.Errorf("feishu token: %w", err)
	}
	if resp != nil && resp.Code != 0 { // do 不判 code——token 面在此显式判（错误带业务码）
		c.token = saved
		return fmt.Errorf("feishu %s: code=%d msg=%s", feishuTokenPath, resp.Code, resp.Msg)
	}
	if out.TenantAccessToken == "" {
		return fmt.Errorf("feishu token: 空口令（code=0）")
	}
	c.token, c.expiry = out.TenantAccessToken, time.Now().Add(time.Duration(out.Expire)*time.Second)
	return nil
}

// ---------- ① 文档枚举（drive folder + wiki space，缺席 = 删除事件） ----------

// feishuFile 云文档列表行。
type feishuFile struct {
	Token        string `json:"token"`
	Type         string `json:"type"` // doc|docx|sheet|...（仅 doc/docx 拉正文）
	Name         string `json:"name"`
	ModifiedTime string `json:"modified_time"` // unix 秒（字符串形态）
	OwnerID      string `json:"owner_id"`
}

// feishuWikiSpace 知识库空间行。
type feishuWikiSpace struct {
	SpaceID string `json:"space_id"`
	Name    string `json:"name"`
}

// feishuWikiNode 知识库节点行（obj_token = 文档 token）。
type feishuWikiNode struct {
	NodeToken   string `json:"node_token"`
	ObjToken    string `json:"obj_token"`
	ObjType     string `json:"obj_type"` // docx|...
	Title       string `json:"title"`
	HasChild    bool   `json:"has_child"`
	ParentToken string `json:"parent_node_token"`
	EditorID    string `json:"editor_id,omitempty"`
	UpdatedAt   string `json:"edited_time,omitempty"`
}

func (c *feishuClient) spaceAllowed(id string) bool {
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

// LoadDocs 全量拉取（首轮）。
func (c *feishuClient) LoadDocs(ctx context.Context) ([]contract.Document, error) {
	return c.listDocs(ctx)
}

// PollDocs 增量轮询：重列全集（缺席 = 删除），cursor = 本轮最大 modified_time。
func (c *feishuClient) PollDocs(ctx context.Context, _ string) ([]contract.Document, string, error) {
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

// listDocs 枚举当前可见全集：drive 空间 + wiki 空间（白名单过滤），doc/docx 拉正文。
func (c *feishuClient) listDocs(ctx context.Context) ([]contract.Document, error) {
	var docs []contract.Document
	// drive：配置了 Spaces = 每个 token 一个目录；未配置 = 全部 wiki 空间（drive 根
	// 需 folder_token，无 token 无法枚举——Spaces 即 drive 目录白名单）
	for _, folder := range c.cfg.Spaces {
		if err := c.listDriveFolder(ctx, folder, &docs); err != nil {
			return nil, err
		}
	}
	// wiki：空间列表（分页）→ 节点树（递归分页）
	var spaces struct {
		Items         []feishuWikiSpace `json:"items"`
		HasMore       bool              `json:"has_more"`
		NextPageToken string            `json:"next_page_token"`
	}
	pageToken := ""
	for {
		q := url.Values{"page_size": {"50"}}
		if pageToken != "" {
			q.Set("page_token", pageToken)
		}
		if err := c.call(ctx, http.MethodGet, feishuWikiSpaces, q, nil, &spaces); err != nil {
			return nil, err
		}
		for _, sp := range spaces.Items {
			if !c.spaceAllowed(sp.SpaceID) {
				continue
			}
			if err := c.listWikiNodes(ctx, sp.SpaceID, "", &docs); err != nil {
				return nil, err
			}
		}
		if !spaces.HasMore || spaces.NextPageToken == "" {
			break
		}
		pageToken = spaces.NextPageToken
	}
	return docs, nil
}

// listDriveFolder 分页枚举单个 drive 目录（token 上的文件）。
func (c *feishuClient) listDriveFolder(ctx context.Context, folder string, docs *[]contract.Document) error {
	pageToken := ""
	for {
		q := url.Values{"folder_token": {folder}, "page_size": {"50"}}
		if pageToken != "" {
			q.Set("page_token", pageToken)
		}
		var out struct {
			Files         []feishuFile `json:"files"`
			HasMore       bool         `json:"has_more"`
			NextPageToken string       `json:"next_page_token"`
		}
		if err := c.call(ctx, http.MethodGet, feishuFilesPath, q, nil, &out); err != nil {
			return err
		}
		for _, f := range out.Files {
			if f.Type != "doc" && f.Type != "docx" {
				continue
			}
			d, err := c.fetchDoc(ctx, f, f.Token, f.Token)
			if err != nil {
				return err
			}
			*docs = append(*docs, d)
		}
		if !out.HasMore || out.NextPageToken == "" {
			return nil
		}
		pageToken = out.NextPageToken
	}
}

// listWikiNodes 递归枚举知识库节点树（has_child → 下钻）。
func (c *feishuClient) listWikiNodes(ctx context.Context, spaceID, parent string, docs *[]contract.Document) error {
	pageToken := ""
	for {
		q := url.Values{"space_id": {spaceID}, "page_size": {"50"}}
		if parent != "" {
			q.Set("parent_node_token", parent)
		}
		if pageToken != "" {
			q.Set("page_token", pageToken)
		}
		var out struct {
			Items         []feishuWikiNode `json:"items"`
			HasMore       bool             `json:"has_more"`
			NextPageToken string           `json:"next_page_token"`
		}
		if err := c.call(ctx, http.MethodGet, feishuWikiNodes, q, nil, &out); err != nil {
			return err
		}
		for _, n := range out.Items {
			if n.ObjType == "docx" {
				f := feishuFile{Token: n.ObjToken, Type: "docx", Name: n.Title, ModifiedTime: n.UpdatedAt, OwnerID: n.EditorID}
				d, err := c.fetchDoc(ctx, f, n.ObjToken, spaceID)
				if err != nil {
					return err
				}
				*docs = append(*docs, d)
			}
			if n.HasChild {
				if err := c.listWikiNodes(ctx, spaceID, n.NodeToken, docs); err != nil {
					return err
				}
			}
		}
		if !out.HasMore || out.NextPageToken == "" {
			return nil
		}
		pageToken = out.NextPageToken
	}
}

// fetchDoc 拉单文档正文（护栏：超 MaxDocBytes 只保 metadata）。
func (c *feishuClient) fetchDoc(ctx context.Context, f feishuFile, externalID, spaceID string) (contract.Document, error) {
	mod, _ := strconv.ParseInt(f.ModifiedTime, 10, 64)
	d := contract.Document{
		Platform:        feishuPlatform,
		ExternalID:      externalID,
		SpaceID:         spaceID,
		Title:           f.Name,
		OwnerID:         f.OwnerID,
		ContentFormat:   feishuContentType,
		SourceUpdatedAt: time.Unix(mod, 0).UTC(),
	}
	var out struct {
		Content string `json:"content"`
	}
	if err := c.call(ctx, http.MethodGet, fmt.Sprintf(feishuContentPath, externalID), nil, nil, &out); err != nil {
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

// ---------- ② 权限成员（每 doc 全量 replace 输入） ----------

// LoadACLs 全量权限元数据：枚举 live 文档 → 逐 doc 权限成员分页。
func (c *feishuClient) LoadACLs(ctx context.Context) ([]contract.DocACL, error) {
	docs, err := c.listDocs(ctx)
	if err != nil {
		return nil, err
	}
	var acls []contract.DocACL
	for _, d := range docs {
		if d.Oversize {
			continue // 未拉正文的 doc 不入权限面（无正文可服务）
		}
		pageToken := ""
		for {
			q := url.Values{"type": {"docx"}, "page_size": {"50"}}
			if pageToken != "" {
				q.Set("page_token", pageToken)
			}
			var out struct {
				Members       []feishuPermMember `json:"members"`
				HasMore       bool               `json:"has_more"`
				NextPageToken string             `json:"next_page_token"`
			}
			if err := c.call(ctx, http.MethodGet, fmt.Sprintf(feishuPermPath, d.ExternalID), q, nil, &out); err != nil {
				return nil, err
			}
			for _, m := range out.Members {
				acls = append(acls, contract.DocACL{
					Platform:   feishuPlatform,
					DocRef:     d.ExternalID,
					MemberType: m.MemberType,
					MemberID:   m.MemberID,
					Perm:       m.perm(),
				})
			}
			if !out.HasMore || out.NextPageToken == "" {
				break
			}
			pageToken = out.NextPageToken
		}
	}
	return acls, nil
}

// feishuPermMember 权限成员行（member_type = user|department；perm view|edit）。
type feishuPermMember struct {
	MemberType string `json:"member_type"`
	MemberID   string `json:"member_id"`
	Perm       string `json:"perm"`
}

func (m feishuPermMember) perm() string {
	if m.Perm == "edit" {
		return "write"
	}
	return "read"
}

// ---------- ③ 通讯录（部门树 + 用户） ----------

// LoadDirectory 全量通讯录：部门树（children 递归）+ 逐部门用户分页。
func (c *feishuClient) LoadDirectory(ctx context.Context) ([]contract.IMContact, []contract.SourceDept, error) {
	var depts []contract.SourceDept
	if err := c.listDepts(ctx, "0", &depts); err != nil {
		return nil, nil, err
	}
	var contacts []contract.IMContact
	// 根部门（id=0）用户 + 各子部门用户
	deptIDs := []string{"0"}
	for _, d := range depts {
		deptIDs = append(deptIDs, d.SourceDeptID)
	}
	for _, deptID := range deptIDs {
		if err := c.listUsers(ctx, deptID, &contacts); err != nil {
			return nil, nil, err
		}
	}
	return contacts, depts, nil
}

// listDepts 递归枚举部门树（department_id 起步，根 = "0"）。
func (c *feishuClient) listDepts(ctx context.Context, deptID string, out *[]contract.SourceDept) error {
	pageToken := ""
	for {
		q := url.Values{"department_id": {deptID}, "page_size": {"50"}}
		if pageToken != "" {
			q.Set("page_token", pageToken)
		}
		var page struct {
			Items         []feishuDept `json:"items"`
			HasMore       bool         `json:"has_more"`
			NextPageToken string       `json:"next_page_token"`
		}
		if err := c.call(ctx, http.MethodGet, feishuDeptsPath, q, nil, &page); err != nil {
			return err
		}
		for _, d := range page.Items {
			*out = append(*out, contract.SourceDept{
				Platform:       feishuPlatform,
				SourceDeptID:   d.DepartmentID,
				Name:           d.Name,
				ParentSourceID: deptID,
			})
			if err := c.listDepts(ctx, d.DepartmentID, out); err != nil {
				return err
			}
		}
		if !page.HasMore || !pageHasMore(page.NextPageToken) {
			return nil
		}
		pageToken = page.NextPageToken
	}
}

type feishuDept struct {
	DepartmentID string `json:"department_id"`
	Name         string `json:"name"`
}

// listUsers 枚举部门内用户（分页）。
func (c *feishuClient) listUsers(ctx context.Context, deptID string, out *[]contract.IMContact) error {
	pageToken := ""
	for {
		q := url.Values{"department_id": {deptID}, "page_size": {"50"}}
		if pageToken != "" {
			q.Set("page_token", pageToken)
		}
		var page struct {
			Items         []feishuUser `json:"items"`
			HasMore       bool         `json:"has_more"`
			NextPageToken string       `json:"next_page_token"`
		}
		if err := c.call(ctx, http.MethodGet, feishuUsersPath, q, nil, &page); err != nil {
			return err
		}
		for _, u := range page.Items {
			*out = append(*out, contract.IMContact{
				Platform: feishuPlatform,
				OpenID:   u.OpenID,
				UnionID:  u.UnionID,
				Email:    u.Email,
				Mobile:   u.Mobile,
				Name:     u.Name,
			})
		}
		if !page.HasMore || !pageHasMore(page.NextPageToken) {
			return nil
		}
		pageToken = page.NextPageToken
	}
}

type feishuUser struct {
	OpenID  string `json:"open_id"`
	UnionID string `json:"union_id"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Mobile  string `json:"mobile"`
}

func pageHasMore(next string) bool { return next != "" }
