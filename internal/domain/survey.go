package domain

import "time"

const SurveyID = "yunnan-202610"

type SurveyQuestion struct {
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Help    string   `json:"help"`
	Options []string `json:"options,omitempty"`
	Max     int      `json:"max,omitempty"`
	Rows    []string `json:"rows,omitempty"`
}

type SurveyAnswer struct {
	Choices []string          `json:"choices,omitempty"`
	Text    string            `json:"text,omitempty"`
	Rows    map[string]string `json:"rows,omitempty"`
}

type SurveySubmission struct {
	Version           int                     `json:"version"`
	Answers           map[string]SurveyAnswer `json:"answers"`
	Analysis          string                  `json:"analysis"`
	Quote             string                  `json:"quote"`
	ContactPermission string                  `json:"contactPermission"`
	Contact           string                  `json:"contact"`
}

type SurveyResponse struct {
	ID        string    `json:"id"`
	Nickname  string    `json:"nickname"`
	CreatedAt time.Time `json:"createdAt"`
	SurveySubmission
}

// These types are private repository state; never return them from HTTP handlers.
type SurveyChallenge struct {
	AnswerHash string    `json:"answerHash"`
	ExpiresAt  time.Time `json:"expiresAt"`
}
type SurveyGuest struct {
	Nickname   string    `json:"nickname"`
	ExpiresAt  time.Time `json:"expiresAt"`
	ResponseID string    `json:"responseId"`
}
type SurveyRate struct {
	Count     int       `json:"count"`
	ExpiresAt time.Time `json:"expiresAt"`
}
type SurveyState struct {
	Settings   SurveySettings             `json:"settings"`
	Challenges map[string]SurveyChallenge `json:"challenges"`
	Guests     map[string]SurveyGuest     `json:"guests"`
	Rates      map[string]SurveyRate      `json:"rates"`
	Responses  map[string]SurveyResponse  `json:"responses"`
}

type SurveySettings struct {
	Open      bool       `json:"open"`
	Version   int        `json:"version"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
	UpdatedBy string     `json:"updatedBy,omitempty"`
}

func SurveyQuestions() []SurveyQuestion {
	return []SurveyQuestion{
		{ID: "q1", Title: "您主要承担什么角色？", Help: "可多选。机构名称、岗位可在补充说明中填写，也可不填。", Options: []string{"制片／项目负责人", "导演／创意", "一线制作", "技术运维", "采购管理", "投资／园区", "院校／文旅", "其他"}},
		{ID: "q2", Title: "最近三个月主要制作什么内容？", Help: "可多选。请补充团队人数与同时在制项目数。", Options: []string{"AI 漫剧／动画短剧", "微短剧", "广告／创意视频", "文旅内容", "教学／研究", "暂无实际项目", "其他"}},
		{ID: "q3", Title: "最近一个项目的交付规模与周期是什么？", Help: "约多少集／镜／分钟，制作周期多少天或周？未知、不便回答可跳过。"},
		{ID: "q4", Title: "从需求到交付，实际经过哪些工具与交接步骤？", Help: "例如模型、ComfyUI、剪辑、网盘、表格、聊天工具，不要求清单完整。"},
		{ID: "q5", Title: "最近一次返工的具体原因是什么？", Help: "可多选，请简述一个真实案例。", Options: []string{"人物／服装变化", "场景／道具不连续", "动作／运镜", "台词／口型／声音", "剪辑节奏", "客户需求变化", "其他", "暂无"}},
		{ID: "q6", Title: "那次修改涉及多少人、几轮、多少时间？", Help: "请估算人数、轮数、人工小时、机器运行／排队小时，以及最耗时环节。未知或不便回答可跳过。"},
		{ID: "q7", Title: "一个月后重做该镜头，哪些资料能找回？", Help: "各项分别选择，不适用可跳过。", Options: []string{"能稳定找回", "不稳定", "通常缺失", "不适用"}, Rows: []string{"参考图", "角色／场景版本", "模型版本", "工作流／节点", "提示词／参数", "声音与音乐", "输出原件", "审批／客户反馈"}},
		{ID: "q8", Title: "目前如何管理版本与交接？", Help: "哪些做法值得保留？哪里最费力？"},
		{ID: "q9", Title: "最近一次环境、模型或节点变化是否影响过交付？", Help: "若有，请补充发生时间、影响及恢复方式。", Options: []string{"没有", "有", "不清楚"}, Max: 1},
		{ID: "q10", Title: "哪些资料可以调用云端，哪些必须留在本地？", Help: "请说明可上云、必须本地／企业存储的资料，以及由谁确定边界；也可写尚无要求或不清楚。"},
		{ID: "q11", Title: "客户实际上要求交付哪些内容？", Help: "区分已要求与希望有，其他要求可补充。", Options: []string{"已要求", "希望有", "暂无要求", "不清楚"}, Rows: []string{"素材文件", "可编辑工程", "版本清单", "来源／许可记录", "审批记录", "AI 标识相关信息"}},
		{ID: "q12", Title: "最希望优先改善哪两项？", Help: "最多两项；也可单选暂无足够痛点。可补充第一、第二优先项。", Options: []string{"生成质量", "版本找回", "批量执行", "环境维护", "素材复用", "审阅协作", "交付整理", "暂无足够痛点", "其他"}, Max: 2},
		{ID: "q13", Title: "哪些条件最可能使您放弃接入新系统？", Help: "可多选，请补充最关键的一个条件。", Options: []string{"迁移成本", "学习成本", "与现有工具不兼容", "数据控制", "性能／稳定性", "价格", "维护负担", "其他"}},
		{ID: "q14", Title: "最近采购类似软件、设备或服务时，谁提出、试用和批准？", Help: "请说明一般周期；投入或可接受预算选填并注明周期。没有经历、不便回答可跳过。"},
		{ID: "q15", Title: "是否愿意提供一个脱敏真实任务继续验证？", Help: "请补充希望验证的结果、可投入角色／时间及开始日期。园区、投资、院校可填写可引荐团队或场景。", Options: []string{"愿意进一步讨论", "需要内部确认", "暂不参与"}, Max: 1},
		{ID: "q16", Title: "您允许我们如何使用本次反馈？", Help: "三项分别选择。录音、录像、公开姓名或作品另行征求同意，本问卷不包含该授权。"},
	}
}
