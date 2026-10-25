package dataset

import "github.com/brianvoe/gofakeit/v7"

// gofakeit tidak punya locale Indonesia.
var indoFirstNames = []string{
	"Adi", "Agus", "Ahmad", "Andi", "Anisa", "Arief", "Ayu", "Bagas", "Bambang", "Bayu",
	"Budi", "Cahya", "Citra", "Dani", "Dedi", "Dewi", "Dian", "Dimas", "Dina", "Eka",
	"Eko", "Endah", "Fajar", "Farah", "Fitri", "Galih", "Gita", "Hadi", "Hana", "Hendra",
	"Indah", "Indra", "Intan", "Irfan", "Joko", "Kartika", "Kevin", "Lestari", "Lina", "Made",
	"Maya", "Mega", "Nadia", "Nanda", "Nur", "Oki", "Putri", "Rahmat", "Rani", "Reza",
	"Rina", "Rizky", "Sari", "Siti", "Sri", "Taufik", "Tika", "Umar", "Vina", "Wahyu",
	"Wayan", "Wulan", "Yanti", "Yoga", "Yudi", "Yusuf", "Zahra", "Ketut", "Nyoman", "Fikri",
}

var indoLastNames = []string{
	"Pratama", "Saputra", "Santoso", "Wijaya", "Hidayat", "Nugroho", "Kusuma", "Setiawan", "Wibowo", "Gunawan",
	"Halim", "Susanto", "Lestari", "Rahmawati", "Wulandari", "Kurniawan", "Hartono", "Salim", "Siregar", "Nasution",
	"Simanjuntak", "Hasibuan", "Lubis", "Harahap", "Sinaga", "Tanjung", "Ginting", "Tarigan", "Sembiring", "Panjaitan",
	"Situmorang", "Manurung", "Firmansyah", "Ramadhan", "Maulana", "Syahputra", "Anggraini", "Permana", "Suryadi", "Hakim",
	"Utomo", "Purnomo", "Sulistyo", "Prasetyo", "Haryanto", "Iskandar", "Sutanto", "Effendi", "Yulianti", "Putri",
}

func IndoName(f *gofakeit.Faker) (first, last string) {
	return Pick(f, indoFirstNames), Pick(f, indoLastNames)
}
