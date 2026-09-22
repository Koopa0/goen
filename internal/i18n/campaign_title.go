package i18n

var (
	KeyAdminCampaignTitleEnLength = key("admin.campaign.title_en_length", Message{ZhHant: "英文活動標題不得超過 60 字。", En: "Use at most 60 characters for the English campaign title."})
	KeyAdminCampaignTitleEn       = key("admin.campaign.title_en", Message{ZhHant: "英文活動標題 (選填)", En: "English campaign title (optional)"})
	KeyAdminCampaignTitleEnHint   = key("admin.campaign.title_en_hint", Message{ZhHant: "留白時,英文頁面會顯示原活動標題。", En: "Leave blank to show the original campaign title on English pages."})
)
