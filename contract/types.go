// 契约 DTO 四件（源端命名空间——语义注释零宿主概念；字段与宿主侧
// 落库类型 1:1，宿主薄适配层做机械转换）。
package contract

import "time"

// Document 是一次拉取产出的文档行。
type Document struct {
	// Platform 平台名（同 Connector.Platform）。
	Platform string
	// ExternalID 源端文档唯一键（宿主侧行主锚：同键重放 = 更新/幂等）。
	ExternalID string
	// SpaceID 源端空间/目录/知识库标识（可为空）。
	SpaceID string
	Title   string
	// OwnerID 源端文档所有者标识（源端命名空间，如 open_id）。
	OwnerID string
	// Raw 源端正文原文（结构保真；超护栏时为 nil，只保 metadata）。
	Raw []byte
	// ContentFormat 正文格式标签（如 feishu-raw-content——宿主解析分派键）。
	ContentFormat string
	// ContentHash Raw 的 sha256 hex（变更检测；adapter 侧计算）。
	ContentHash string
	SizeBytes   int64
	// Oversize 超单文档护栏（只保 metadata，正文不传）。
	Oversize bool
	// SourceUpdatedAt 源端最后修改时间（宿主侧游标水位输入）。
	SourceUpdatedAt time.Time
	// Deleted 源端删除事件（增量轮缺席由宿主 diff 收敛；本字段供显式删除信号）。
	Deleted bool
}

// DocACL 是源端文档权限元数据行（宿主 replace 收敛的输入面，源端命名空间——
// 成员展开/丢弃归宿主落库侧）。
type DocACL struct {
	Platform string
	// DocRef 源端文档 external_id。
	DocRef string
	// MemberType 源端成员类型（user|dept）。
	MemberType string
	// MemberID 源端成员键（open_id / source_dept_id——源端命名空间）。
	MemberID string
	// Perm 权限（read|write）。
	Perm string
}

// IMContact 是源端通讯录联系人行（宿主侧身份映射物料：email/mobile
// 为与宿主用户目录精确匹配的两把钥匙）。
type IMContact struct {
	Platform string
	OpenID   string
	UnionID  string
	Email    string
	Mobile   string
	Name     string
}

// SourceDept 是源端部门树行（宿主侧部门落地/ACL 解析物料）。
type SourceDept struct {
	Platform string
	// SourceDeptID 源端部门键。
	SourceDeptID string
	Name         string
	// ParentSourceID 父部门源端键（根部门 = 源端约定的根值）。
	ParentSourceID string
}
