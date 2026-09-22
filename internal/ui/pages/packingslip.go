package pages

// PackingSlipView deliberately carries no money, invoice, email or note fields.
type PackingSlipView struct {
	Number, Recipient, Phone, Destination, ShippingName string
	Lines                                               []PackingSlipLine
}

// PackingSlipLine contains the ordered quantity, not a shipment allocation.
type PackingSlipLine struct {
	SKU, Name, Label, Quantity string
}

// PackingSlipFromOrder is the privacy boundary between the order and its printout.
func PackingSlipFromOrder(v *AdminOrderView) PackingSlipView {
	slip := PackingSlipView{Number: v.Number, Recipient: v.Recipient, Phone: v.Phone, Destination: v.Address, ShippingName: v.ShippingName}
	for _, line := range v.Lines {
		slip.Lines = append(slip.Lines, PackingSlipLine{SKU: line.SKU, Name: line.Name, Label: line.Label, Quantity: line.QuantityText()})
	}
	return slip
}
