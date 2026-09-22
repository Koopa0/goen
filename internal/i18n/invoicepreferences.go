package i18n

var (
	KeyInvoiceCitizen          = key("invoice.citizen", Message{ZhHant: "自然人憑證載具", En: "Citizen certificate carrier"})
	KeyInvoiceDonate           = key("invoice.donate", Message{ZhHant: "捐贈發票", En: "Donate invoice"})
	KeyCitizenCarrierMalformed = key("invoice.citizen.invalid", Message{ZhHant: "請輸入 2 碼大寫英文字母及 14 碼數字的自然人憑證條碼。", En: "Enter a citizen certificate barcode with 2 uppercase letters and 14 digits."})
	KeyDonationCodeMalformed   = key("invoice.donation.invalid", Message{ZhHant: "請輸入 3 至 7 碼數字的捐贈碼。", En: "Enter a donation code containing 3 to 7 digits."})
	KeyFieldDonationCode       = key("field.invoice.donation", Message{ZhHant: "受贈單位捐贈碼", En: "Recipient donation code"})
	KeyDonationCodeHelp        = key("invoice.donation.help", Message{ZhHant: "請向受贈單位確認捐贈碼；開頭的 0 會保留。捐贈發票不能同時填入統編或載具。", En: "Confirm the code with the recipient organization. Leading zeroes are preserved. A donation invoice cannot also use a tax ID or carrier."})
	KeyAdminCarrierCitizen     = key("admin.carrier.citizen", Message{ZhHant: "自然人憑證載具：%s", En: "Citizen certificate carrier: %s"})
	KeyAdminInvoiceDonate      = key("admin.invoice.donate", Message{ZhHant: "發票捐贈碼：%s", En: "Invoice donation code: %s"})
)
