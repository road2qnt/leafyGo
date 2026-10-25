package dataset

import (
	"fmt"
	"strings"
	"time"

	"github.com/brianvoe/gofakeit/v7"
)

// Satu dataset dipakai semua target agar id produk, customer, dan order konsisten.
type Dataset struct {
	Now        time.Time
	Categories []Category
	Products   []Product
	Customers  []Customer
	Addresses  []Address
	Orders     []Order
	Reviews    []Review
}

type Category struct {
	ID          int
	Name        string
	Slug        string
	Description string
}

type Product struct {
	ID          int
	SKU         string
	Name        string
	CategoryID  int
	PriceCents  int
	CostCents   int
	WeightGrams int
	IsActive    bool
	Description *string
	CreatedAt   time.Time
}

type Customer struct {
	ID        int
	FirstName string
	LastName  string
	Email     string
	Phone     *string
	OptIn     bool
	CreatedAt time.Time
	State     string
}

type Address struct {
	ID         int
	CustomerID int
	Label      string
	Line1      string
	City       string
	State      string
	PostalCode string
	IsDefault  bool
}

type OrderItem struct {
	ProductID      int
	Quantity       int
	UnitPriceCents int
}

type Order struct {
	ID            int
	CustomerID    int
	AddressID     int
	Status        string
	PlacedAt      time.Time
	Items         []OrderItem
	SubtotalCents int
	ShippingCents int
	TaxCents      int
	TotalCents    int
}

type Review struct {
	ID         int
	ProductID  int
	CustomerID int
	Rating     int
	Title      string
	Body       string
	CreatedAt  time.Time
}

type item struct {
	noun     string
	min, max int
}

// Nama produk = modifier + item, harga mengikuti item.
var categorySeeds = []struct {
	name, desc string
	modifiers  []string
	items      []item
}{
	{"Kitchen", "Cookware, utensils, and small appliances.",
		[]string{"Cast Iron", "Stainless", "Bamboo", "Ceramic", "Nonstick", "Copper", "Walnut", "Pro"},
		[]item{{"Skillet", 25, 90}, {"Chef's Knife", 30, 160}, {"Cutting Board", 15, 70}, {"Pour-Over Kettle", 30, 110}, {"Mixing Bowl Set", 20, 65}, {"Dutch Oven", 60, 220}, {"Utensil Set", 12, 45}, {"Measuring Cups", 8, 25}}},
	{"Home Decor", "Frames, rugs, vases, and accents.",
		[]string{"Linen", "Rattan", "Marble", "Woven", "Velvet", "Terracotta", "Nordic", "Vintage"},
		[]item{{"Throw Pillow", 18, 60}, {"Area Rug", 80, 340}, {"Wall Mirror", 45, 220}, {"Vase", 15, 85}, {"Photo Frame", 12, 45}, {"Throw Blanket", 30, 120}, {"Candle Holder", 10, 40}}},
	{"Garden", "Planters, tools, seeds, and outdoor care.",
		[]string{"Galvanized", "Cedar", "Ceramic", "Self-Watering", "Heirloom", "Compact", "Heavy-Duty"},
		[]item{{"Planter", 15, 90}, {"Pruning Shears", 14, 48}, {"Watering Can", 18, 55}, {"Raised Garden Bed", 60, 180}, {"Seed Collection", 6, 24}, {"Hose Reel", 35, 120}, {"Trowel", 8, 22}}},
	{"Electronics", "Audio, chargers, and smart home devices.",
		[]string{"Wireless", "Bluetooth", "Smart", "Portable", "Noise-Canceling", "Fast-Charge", "Mini"},
		[]item{{"Speaker", 35, 250}, {"Headphones", 45, 380}, {"Charging Stand", 20, 70}, {"Power Bank", 25, 90}, {"Smart Plug", 12, 35}, {"Earbuds", 30, 220}, {"Desk Lamp", 30, 120}}},
	{"Office", "Desk accessories, paper, and organizers.",
		[]string{"Leather", "Recycled", "Minimal", "Oak", "Aluminum", "Dotted", "Ergonomic"},
		[]item{{"Notebook", 6, 28}, {"Desk Organizer", 15, 60}, {"Monitor Stand", 35, 140}, {"Pen Set", 8, 45}, {"Office Chair Cushion", 25, 70}, {"File Cabinet", 90, 260}, {"Planner", 12, 35}}},
	{"Outdoor", "Camping, hiking, and picnic gear.",
		[]string{"Ultralight", "Waterproof", "Insulated", "Packable", "Trail", "All-Season"},
		[]item{{"Tent", 120, 420}, {"Sleeping Bag", 60, 240}, {"Daypack", 45, 160}, {"Camp Chair", 30, 110}, {"Water Bottle", 15, 45}, {"Headlamp", 18, 60}, {"Picnic Blanket", 25, 75}}},
	{"Fitness", "Mats, weights, and recovery gear.",
		[]string{"Adjustable", "Cork", "Non-Slip", "Foam", "Resistance", "Studio"},
		[]item{{"Yoga Mat", 20, 95}, {"Dumbbell Set", 45, 260}, {"Foam Roller", 15, 50}, {"Jump Rope", 8, 30}, {"Resistance Bands", 12, 40}, {"Kettlebell", 30, 120}, {"Massage Gun", 70, 260}}},
	{"Bath", "Towels, organizers, and bath care.",
		[]string{"Turkish Cotton", "Waffle Weave", "Bamboo", "Eucalyptus", "Plush", "Teak"},
		[]item{{"Bath Towel", 18, 55}, {"Bath Mat", 20, 60}, {"Shower Caddy", 18, 50}, {"Bath Robe", 45, 120}, {"Soap Dispenser", 10, 32}, {"Body Wash", 6, 22}}},
	{"Pets", "Beds, bowls, toys, and grooming.",
		[]string{"Orthopedic", "Chew-Proof", "Stainless", "Washable", "Calming", "Reflective"},
		[]item{{"Dog Bed", 35, 150}, {"Food Bowl", 10, 35}, {"Leash", 12, 40}, {"Cat Tree", 50, 180}, {"Grooming Brush", 8, 28}, {"Chew Toy", 5, 20}}},
	{"Toys", "Games, puzzles, and building sets.",
		[]string{"Wooden", "Classic", "Magnetic", "Deluxe", "Junior", "Family"},
		[]item{{"Puzzle", 12, 40}, {"Building Set", 25, 140}, {"Board Game", 20, 60}, {"Train Set", 35, 120}, {"Art Kit", 15, 45}, {"Plush Bear", 10, 35}}},
	{"Pantry", "Coffee, tea, snacks, and spices.",
		[]string{"Organic", "Single-Origin", "Small-Batch", "Smoked", "Wildflower", "Cold-Pressed"},
		[]item{{"Coffee Beans", 12, 28}, {"Green Tea", 8, 22}, {"Olive Oil", 12, 35}, {"Honey", 9, 24}, {"Spice Sampler", 15, 40}, {"Granola", 6, 14}}},
	{"Lighting", "Bulbs, string lights, and fixtures.",
		[]string{"Dimmable", "Edison", "Solar", "Warm White", "Brass", "Paper"},
		[]item{{"String Lights", 15, 60}, {"Pendant Light", 60, 290}, {"Floor Lamp", 70, 260}, {"LED Bulb 4-Pack", 10, 30}, {"Table Lamp", 35, 150}, {"Wall Sconce", 45, 180}}},
}

var emailDomains = []string{"gmail.com", "outlook.com", "yahoo.com", "icloud.com", "proton.me", "hotmail.com"}

func slug(s string) string {
	return strings.ToLower(strings.ReplaceAll(s, " ", "-"))
}

func Generate(f *gofakeit.Faker, now time.Time) *Dataset {
	d := &Dataset{Now: now}

	for i, c := range categorySeeds {
		d.Categories = append(d.Categories, Category{ID: i + 1, Name: c.name, Slug: slug(c.name), Description: c.desc})
	}

	names := map[string]bool{}
	for i := 1; i <= 240; i++ {
		ci := f.IntRange(0, len(categorySeeds)-1)
		c := categorySeeds[ci]
		it := Pick(f, c.items)
		name := Pick(f, c.modifiers) + " " + it.noun
		for names[name] {
			it = Pick(f, c.items)
			name = Pick(f, c.modifiers) + " " + it.noun
		}
		names[name] = true
		price := f.IntRange(it.min, it.max)*100 - Pick(f, []int{0, 1, 1, 5, 51})
		p := Product{
			ID: i, SKU: fmt.Sprintf("GL-%05d", 10000+i*7), Name: name, CategoryID: ci + 1,
			PriceCents: price, CostCents: price * f.IntRange(38, 68) / 100,
			WeightGrams: f.IntRange(80, 9000), IsActive: f.IntRange(1, 100) > 6,
			CreatedAt: now.AddDate(0, 0, -f.IntRange(420, 900)),
		}
		if f.IntRange(1, 100) > 15 {
			p.Description = Ptr(describe(f, name, it.noun))
		}
		d.Products = append(d.Products, p)
	}

	emails := map[string]bool{}
	addrID := 0
	for i := 1; i <= 600; i++ {
		first, last := IndoName(f)
		email := fmt.Sprintf("%s.%s@%s", strings.ToLower(first), strings.ToLower(last), Pick(f, emailDomains))
		for n := 2; emails[email]; n++ {
			email = fmt.Sprintf("%s.%s%d@%s", strings.ToLower(first), strings.ToLower(last), n, Pick(f, emailDomains))
		}
		emails[email] = true
		c := Customer{
			ID: i, FirstName: first, LastName: last, Email: email,
			OptIn: f.IntRange(1, 100) <= 42, CreatedAt: now.Add(-time.Duration(f.IntRange(5*24, 3*365*24)) * time.Hour),
		}
		if f.IntRange(1, 100) <= 86 {
			c.Phone = Ptr(f.PhoneFormatted())
		}
		n := 1
		if f.IntRange(1, 100) <= 30 {
			n = 2
		}
		for j := 0; j < n; j++ {
			addrID++
			a := f.Address()
			label := "Home"
			if j == 1 {
				label = Pick(f, []string{"Work", "Parents", "Vacation home"})
			}
			if j == 0 {
				c.State = f.StateAbr()
			}
			d.Addresses = append(d.Addresses, Address{
				ID: addrID, CustomerID: i, Label: label, Line1: a.Street, City: a.City,
				State: c.State, PostalCode: a.Zip, IsDefault: j == 0,
			})
		}
		d.Customers = append(d.Customers, c)
	}
	defaultAddr := map[int]int{}
	for _, a := range d.Addresses {
		if a.IsDefault {
			defaultAddr[a.CustomerID] = a.ID
		}
	}

	reviewed := map[[2]int]bool{}
	window := now.AddDate(0, 0, -400)
	for i := 1; i <= 2400; i++ {
		c := Pick(f, d.Customers)
		start := c.CreatedAt
		if start.Before(window) {
			start = window
		}
		// Order lebih padat di bulan-bulan terakhir.
		placed := now.Add(-time.Duration(Skewed(f, 1.5) * float64(now.Sub(start))))
		o := Order{ID: i, CustomerID: c.ID, AddressID: defaultAddr[c.ID], PlacedAt: placed.Truncate(time.Second), Status: orderStatus(f, now.Sub(placed))}
		seen := map[int]bool{}
		for n := f.IntRange(1, 5); len(o.Items) < n; {
			p := Pick(f, d.Products)
			if seen[p.ID] || !p.IsActive {
				continue
			}
			seen[p.ID] = true
			it := OrderItem{ProductID: p.ID, Quantity: Pick(f, []int{1, 1, 1, 2, 2, 3}), UnitPriceCents: p.PriceCents}
			o.Items = append(o.Items, it)
			o.SubtotalCents += it.Quantity * it.UnitPriceCents
		}
		if o.SubtotalCents < 7500 {
			o.ShippingCents = 699
		}
		o.TaxCents = o.SubtotalCents * 825 / 10000
		o.TotalCents = o.SubtotalCents + o.ShippingCents + o.TaxCents
		d.Orders = append(d.Orders, o)

		if o.Status != "delivered" {
			continue
		}
		for _, it := range o.Items {
			key := [2]int{it.ProductID, c.ID}
			if reviewed[key] || f.IntRange(1, 100) > 35 {
				continue
			}
			reviewed[key] = true
			at := o.PlacedAt.Add(time.Duration(f.IntRange(5*24, 30*24)) * time.Hour)
			if at.After(now) {
				at = now.Add(-time.Hour)
			}
			d.Reviews = append(d.Reviews, Review{
				ID: len(d.Reviews) + 1, ProductID: it.ProductID, CustomerID: c.ID,
				Rating: Pick(f, []int{5, 5, 5, 5, 4, 4, 4, 3, 3, 2, 1}),
				Title:  strings.TrimSuffix(f.Sentence(), "."), Body: f.Comment(), CreatedAt: at.Truncate(time.Second),
			})
		}
	}
	return d
}

var descriptionTemplates = []string{
	"The %[1]s is built for daily use and made to last.",
	"Our best-selling %[2]s, refined with customer feedback.",
	"A dependable %[2]s that looks as good as it works.",
	"Thoughtfully designed, the %[1]s fits right into any routine.",
	"Upgrade to the %[1]s for better quality at a fair price.",
}

var descriptionExtras = []string{
	"Ships in recyclable packaging.",
	"Backed by a one-year warranty.",
	"Free returns within 30 days.",
	"Easy to clean and store.",
	"Designed in Portland, Oregon.",
	"Rated highly by repeat customers.",
}

func describe(f *gofakeit.Faker, name, noun string) string {
	return fmt.Sprintf(Pick(f, descriptionTemplates), name, strings.ToLower(noun)) + " " + Pick(f, descriptionExtras)
}

func orderStatus(f *gofakeit.Faker, age time.Duration) string {
	days := age.Hours() / 24
	r := f.IntRange(1, 100)
	switch {
	case days < 2:
		if r <= 30 {
			return "pending"
		}
		return "paid"
	case days < 7:
		if r <= 20 {
			return "paid"
		}
		return "shipped"
	default:
		switch {
		case r <= 85:
			return "delivered"
		case r <= 93:
			return "cancelled"
		default:
			return "refunded"
		}
	}
}
