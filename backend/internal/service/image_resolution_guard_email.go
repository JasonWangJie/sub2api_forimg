package service

const NotificationEmailEventImageResolutionMismatch = "image.resolution_mismatch"

func init() {
	notificationEmailEventOrder = append(notificationEmailEventOrder, NotificationEmailEventImageResolutionMismatch)
	notificationEmailEventDefinitions[NotificationEmailEventImageResolutionMismatch] = NotificationEmailEventInfo{
		Event: NotificationEmailEventImageResolutionMismatch, Label: "Image resolution downgrade", Description: "Sent to administrators when decoded image pixels fall below the configured requested resolution threshold.", Category: "admin", Optional: false,
		Placeholders: append(append([]string{}, notificationEmailCommonPlaceholders...), "account_id", "account_name", "platform", "requested_model", "upstream_model", "requested_tier", "expected_dimensions", "actual_dimensions", "area_percent", "min_area_percent", "task_id", "request_id", "cooldown_until"),
	}
	notificationEmailOfficialTemplates[NotificationEmailEventImageResolutionMismatch] = map[string]notificationEmailOfficialTemplate{
		notificationEmailDefaultLocale: {Subject: "[{{site_name}}] Image resolution downgrade - {{account_name}}", HTML: notificationEmailCard("#d97706", "Image resolution downgrade", `
<p>Decoded image pixels were below the configured threshold. The current image is delivered normally; this account's same model and resolution are temporarily paused for subsequent image requests.</p>
<p>Account: {{account_name}} (#{{account_id}}), {{platform}}</p>
<p>Requested model: {{requested_model}}; upstream model: {{upstream_model}}</p>
<p>Requested resolution: {{requested_tier}}, target {{expected_dimensions}}</p>
<p>Actual dimensions: {{actual_dimensions}}; target area: {{area_percent}}; minimum: {{min_area_percent}}%</p>
<p>Task: {{task_id}}; request: {{request_id}}</p>
<p>Cooling until (UTC): {{cooldown_until}}</p>`)},
		notificationEmailLocaleChinese: {Subject: "[{{site_name}}] 生图分辨率降档告警 - {{account_name}}", HTML: notificationEmailCard("#d97706", "生图分辨率降档告警", `
<p>成品真实像素量低于配置阈值。当前图片仍正常交付；该账号的同模型、同分辨率已暂停后续同步和异步生图调度。</p>
<p>账号：{{account_name}}（#{{account_id}}），平台：{{platform}}</p>
<p>请求模型：{{requested_model}}；转发模型：{{upstream_model}}</p>
<p>请求档位：{{requested_tier}}；目标尺寸：{{expected_dimensions}}</p>
<p>实际尺寸：{{actual_dimensions}}；目标像素占比：{{area_percent}}；最低要求：{{min_area_percent}}%</p>
<p>任务：{{task_id}}；请求：{{request_id}}</p>
<p>冷却截止（UTC）：{{cooldown_until}}</p>`)},
	}
}
