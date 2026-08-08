package main

// A scenario is one prev/curr document pair, isolating a specific defect (or
// small combination of related defects — some numbering rules legitimately
// fire together, and that is left visible rather than engineered away).
type scenario struct {
	name string
	desc string
	prev []string
	curr []string
}

// idTail is the closing chapter appended to every Indonesian-style scenario,
// padding each document past three pages the way a real cooperation
// agreement's boilerplate would. Pasal numbers are placeholders, rewritten
// per scenario/side by renumberHeadings.
var idTail = []string{
	"BAB VI",
	"KETENTUAN LAIN-LAIN DAN PENUTUP",
	"",
	"Pasal 900",
	"Pihak Kedua wajib menutup asuransi tanggung jawab hukum pihak ketiga dan asuransi kecelakaan kerja bagi seluruh tenaga kerja yang ditugaskan pada kegiatan penambangan sebagaimana diatur dalam perjanjian ini, dengan nilai pertanggungan paling rendah sesuai dengan ketentuan peraturan perundang-undangan yang berlaku di bidang ketenagakerjaan dan pertambangan.",
	"",
	"Pasal 900",
	"(1) Pihak Kedua bertanggung jawab penuh atas segala kerugian yang timbul akibat kelalaian dalam pelaksanaan pekerjaan, termasuk namun tidak terbatas pada kerusakan peralatan, pencemaran lingkungan, dan cedera pada tenaga kerja.",
	"(2) Tanggung jawab sebagaimana dimaksud pada ayat (1) tidak berlaku apabila kerugian timbul akibat instruksi tertulis dari Pihak Pertama yang terbukti keliru di kemudian hari.",
	"",
	"Pasal 900",
	"Masing-masing pihak wajib menjaga kerahasiaan seluruh informasi, data teknis, dan dokumen yang diperoleh sehubungan dengan pelaksanaan perjanjian ini, dan tidak boleh mengungkapkannya kepada pihak ketiga tanpa persetujuan tertulis dari pihak lainnya, kecuali diwajibkan oleh peraturan perundang-undangan atau perintah pengadilan yang berwenang.",
	"",
	"Pasal 900",
	"(1) Hak dan kewajiban yang timbul dari perjanjian ini tidak dapat dialihkan kepada pihak lain tanpa persetujuan tertulis terlebih dahulu dari pihak lainnya.",
	"(2) Setiap pengalihan yang dilakukan tanpa persetujuan sebagaimana dimaksud pada ayat (1) dinyatakan batal demi hukum dan tidak mengikat pihak yang dirugikan.",
	"",
	"Pasal 900",
	"Segala pemberitahuan yang berkaitan dengan pelaksanaan perjanjian ini wajib disampaikan secara tertulis ke alamat yang tercantum dalam bagian pembuka perjanjian, dan dianggap telah diterima pada saat diserahkan langsung atau tiga hari kerja setelah dikirimkan melalui pos tercatat dengan bukti penerimaan.",
	"",
	"Pasal 900",
	"Perjanjian ini tunduk pada dan ditafsirkan berdasarkan hukum Negara Republik Indonesia, dan setiap perselisihan yang tidak dapat diselesaikan secara musyawarah akan diselesaikan melalui Badan Arbitrase Nasional Indonesia sesuai dengan peraturan dan prosedur yang berlaku pada saat pengajuan permohonan arbitrase.",
	"",
	"Pasal 900",
	"(1) Setiap perubahan atau penambahan atas ketentuan dalam perjanjian ini hanya sah apabila dibuat secara tertulis dan ditandatangani oleh kedua belah pihak dalam bentuk adendum.",
	"(2) Adendum sebagaimana dimaksud pada ayat (1) merupakan satu kesatuan yang tidak terpisahkan dari perjanjian ini dan berlaku sejak tanggal ditandatangani oleh para pihak.",
	"",
	"Pasal 900",
	"Apabila salah satu ketentuan dalam perjanjian ini dinyatakan batal atau tidak dapat dilaksanakan oleh pejabat yang berwenang, maka ketentuan lainnya tetap berlaku penuh dan mengikat para pihak sebagaimana mestinya.",
	"",
	"Pasal 900",
	"Perjanjian ini dibuat dalam Bahasa Indonesia. Apabila di kemudian hari dibuat pula terjemahan dalam bahasa lain untuk kemudahan referensi, maka versi Bahasa Indonesia yang berlaku dalam hal terjadi perbedaan penafsiran antara kedua versi.",
	"",
	"Pasal 900",
	"Perjanjian ini ditandatangani dalam rangkap 2 (dua) asli, masing-masing bermeterai cukup dan mempunyai kekuatan hukum yang sama, untuk dipegang oleh masing-masing pihak sebagai pegangan.",
}

// enTail is the English-language equivalent, used by the contract-style
// scenario so the closing boilerplate matches the rest of that document.
var enTail = []string{
	"Article 900",
	"Insurance and Liability",
	"",
	"900.1 The Contractor shall maintain public liability insurance and workers' compensation insurance for all personnel assigned to the Mining Area throughout the term of this Agreement, in amounts not less than those required by applicable law.",
	"900.2 Each party shall be liable for losses arising from its own negligence in the performance of this Agreement, save where such loss arises from a written instruction of the other party later shown to be erroneous.",
	"",
	"Article 900",
	"Confidentiality",
	"",
	"900.1 Each party shall keep confidential all information, technical data, and documents obtained in connection with this Agreement, and shall not disclose the same to any third party without the prior written consent of the other party, except as required by law or court order.",
	"",
	"Article 900",
	"Assignment",
	"",
	"900.1 Neither party may assign its rights or obligations under this Agreement without the prior written consent of the other party, and any purported assignment in breach of this Clause shall be void.",
	"",
	"Article 900",
	"Governing Law and Dispute Resolution",
	"",
	"900.1 This Agreement shall be governed by and construed in accordance with the laws of the Republic of Indonesia.",
	"900.2 Any dispute arising out of or in connection with this Agreement that cannot be resolved amicably shall be referred to and finally resolved by arbitration administered by the Indonesian National Board of Arbitration.",
	"",
	"Article 900",
	"Amendment and Severability",
	"",
	"900.1 No amendment to this Agreement shall be effective unless made in writing and signed by both parties.",
	"900.2 If any provision of this Agreement is held invalid or unenforceable, the remaining provisions shall continue in full force and effect.",
	"",
	"Article 900",
	"Counterparts",
	"",
	"900.1 This Agreement is executed in two originals, each of which shall be deemed an original and both of which together shall constitute one and the same instrument.",
}

func scenarios() []scenario {
	return []scenario{
		textOnlyScenario(),
		renumberingScenario(),
		brokenReferenceScenario(),
		relocatedReferenceScenario(),
		contractStyleScenario(),
		kitchenSinkScenario(),
	}
}

// --- 1. text_only ---------------------------------------------------------

func textOnlyScenario() scenario {
	base := []string{
		"BAB I",
		"KETENTUAN UMUM",
		"",
		"Pasal 1",
		"Dalam Perjanjian Kerja Sama Operasional Pertambangan ini yang dimaksud dengan:",
		"1. Wilayah Izin Usaha Pertambangan, yang selanjutnya disingkat WIUP, adalah wilayah yang diberikan kepada pemegang izin usaha pertambangan untuk melaksanakan kegiatan usaha pertambangan.",
		"2. Pihak Pertama adalah badan usaha pemegang Izin Usaha Pertambangan Operasi Produksi yang bertindak selaku pemberi pekerjaan dalam perjanjian ini.",
		"3. Pihak Kedua adalah badan usaha yang bertindak selaku kontraktor pelaksana kegiatan penambangan berdasarkan perjanjian kerja sama operasional ini.",
		"4. Kegiatan Pertambangan adalah sebagian atau seluruh tahapan kegiatan pengupasan lapisan tanah penutup, penambangan, pengangkutan, dan penimbunan hasil tambang di lokasi yang telah ditentukan bersama.",
		"5. Rencana Kerja dan Anggaran Biaya, yang selanjutnya disingkat RKAB, adalah dokumen perencanaan tahunan yang memuat target produksi dan anggaran biaya pelaksanaan pekerjaan.",
		"",
		"BAB II",
		"HAK DAN KEWAJIBAN PARA PIHAK",
		"",
		"Pasal 2",
		"(1) Pihak Pertama berhak menerima laporan produksi harian dan laporan keuangan bulanan dari Pihak Kedua selama jangka waktu pelaksanaan pekerjaan.",
		"(2) Pihak Kedua berhak memperoleh akses terhadap seluruh data geologi dan data teknis yang dimiliki oleh Pihak Pertama sepanjang diperlukan untuk pelaksanaan pekerjaan penambangan.",
		"(3) Dalam melaksanakan kegiatan sebagaimana dimaksud pada ayat (2), Pihak Kedua wajib:",
		"a. mematuhi seluruh standar operasional prosedur pertambangan yang ditetapkan oleh Pihak Pertama;",
		"b. menjaga kerahasiaan seluruh dokumen teknis dan informasi bisnis yang diperoleh selama pelaksanaan pekerjaan;",
		"c. melaporkan setiap kejadian kecelakaan kerja kepada Pihak Pertama paling lambat 1 (satu) hari kerja sejak kejadian.",
		"",
		"Pasal 3",
		"(1) Pihak Kedua wajib menyediakan tenaga kerja, peralatan, dan bahan yang diperlukan untuk pelaksanaan kegiatan penambangan sesuai dengan RKAB yang telah disetujui oleh Pihak Pertama.",
		"(2) Seluruh peralatan sebagaimana dimaksud pada ayat (1) harus memenuhi standar keselamatan dan kesehatan kerja pertambangan sesuai dengan peraturan perundang-undangan yang berlaku.",
		"",
		"Pasal 4",
		"(1) Pihak Pertama wajib membayar imbalan jasa penambangan kepada Pihak Kedua sesuai dengan tarif yang disepakati dalam Lampiran Perjanjian ini.",
		"(2) Pembayaran sebagaimana dimaksud pada ayat (1) dilakukan paling lambat 14 (empat belas) hari kerja setelah faktur diterima secara lengkap dan benar oleh Pihak Pertama.",
		"",
		"BAB III",
		"JANGKA WAKTU DAN ISTILAH KHUSUS",
		"",
		"Pasal 5",
		"(1) Jangka waktu penyelesaian setiap tahap pekerjaan pengupasan lapisan tanah penutup adalah 30 (tiga puluh) hari kerja sejak diterbitkannya perintah kerja oleh Pihak Pertama.",
		"(2) Keterlambatan penyelesaian pekerjaan sebagaimana dimaksud pada ayat (1) dikenakan denda keterlambatan sebesar 1 (satu) permil per hari dari nilai kontrak, dengan denda maksimum sebesar 5% (lima persen) dari nilai kontrak.",
		"",
		"Pasal 6",
		"Pembayaran dilakukan secara bertahap sesuai dengan progres pekerjaan yang dibuktikan dengan berita acara serah terima pekerjaan yang ditandatangani oleh kedua belah pihak.",
		"",
		"Pasal 7",
		"Kewajiban Pihak Pertama untuk menyediakan lahan kerja yang aman dan bebas dari gangguan pihak ketiga berlaku sejak tanggal penandatanganan perjanjian ini sampai dengan berakhirnya jangka waktu perjanjian.",
		"",
		"BAB IV",
		"KEADAAN MEMAKSA",
		"",
		"Pasal 8",
		"(1) Force majeure adalah peristiwa di luar kemampuan wajar para pihak yang menyebabkan tidak dapat dilaksanakannya kewajiban sebagaimana mestinya, termasuk namun tidak terbatas pada bencana alam, kerusuhan, dan perubahan kebijakan pemerintah yang berdampak langsung pada pelaksanaan pekerjaan.",
		"(2) Pihak yang mengalami force majeure wajib memberitahukan pihak lainnya secara tertulis paling lambat 7 (tujuh) hari kalender sejak terjadinya peristiwa tersebut, disertai dengan bukti pendukung yang relevan.",
		"",
		"Pasal 9",
		"Setiap perselisihan yang timbul dari pelaksanaan perjanjian ini akan diselesaikan terlebih dahulu secara musyawarah untuk mufakat oleh kedua belah pihak dalam jangka waktu paling lama 30 (tiga puluh) hari kalender.",
	}

	// base runs BAB I..IV, so the shared tail continues at BAB V.
	tail := renumberChapter(renumberHeadings(idTail, "Pasal ", 10), 5)

	prev := append(append([]string{}, base...), tail...)

	curr := append([]string{}, base...)
	// Substantive change: a real term, not punctuation — must NOT be cosmetic.
	curr[34] = "(1) Jangka waktu penyelesaian setiap tahap pekerjaan pengupasan lapisan tanah penutup adalah 60 (enam puluh) hari kerja sejak diterbitkannya perintah kerja oleh Pihak Pertama."
	// Cosmetic change: punctuation only — must be flagged cosmetic and excluded
	// from the substantive set.
	curr[38] = "Pembayaran dilakukan secara bertahap, sesuai dengan progres pekerjaan yang dibuktikan dengan berita acara serah terima pekerjaan yang ditandatangani oleh kedua belah pihak."
	// Case-only change: a defined term becomes a common noun — must NOT be
	// treated as cosmetic even though the words are otherwise identical.
	curr[41] = "Kewajiban pihak pertama untuk menyediakan lahan kerja yang aman dan bebas dari gangguan pihak ketiga berlaku sejak tanggal penandatanganan perjanjian ini sampai dengan berakhirnya jangka waktu perjanjian."
	curr = append(curr, tail...)

	return scenario{
		name: "text_only",
		desc: "Perubahan teks murni: substantif, kosmetik (tanda baca), dan case-only (istilah terdefinisi) — tanpa perubahan struktur.",
		prev: prev,
		curr: curr,
	}
}

// --- 2. renumbering --------------------------------------------------------

func renumberingScenario() scenario {
	prevBody := []string{
		"BAB I",
		"KETENTUAN UMUM",
		"",
		"Pasal 1",
		"Peraturan ini mengatur tata cara pelaksanaan reklamasi dan pascatambang pada wilayah izin usaha pertambangan mineral dan batubara sesuai dengan rencana yang telah disetujui.",
		"",
		"BAB II",
		"TAHAPAN REKLAMASI",
		"",
		"Pasal 2",
		"(1) Pemegang izin wajib menyusun rencana reklamasi sebagai bagian dari dokumen RKAB paling lambat pada tahun pertama kegiatan operasi produksi.",
		"(2) Rencana reklamasi sebagaimana dimaksud pada ayat (1) memuat rencana penataan lahan, pengendalian erosi, dan pengelolaan kualitas air permukaan.",
		"(3) Pelaksanaan reklamasi dilakukan secara bertahap sesuai dengan kemajuan tambang dan dilaporkan setiap semester kepada instansi yang berwenang.",
		"",
		"Pasal 3",
		"Kegiatan reklamasi sebagaimana dimaksud dalam Pasal 2 meliputi:",
		"a. penataan kembali permukaan lahan bekas tambang sesuai dengan peruntukan akhir yang direncanakan;",
		"b. penanaman tanaman penutup tanah dan tanaman produktif sesuai dengan peruntukan lahan pascatambang;",
		"c. pemantauan kualitas air dan udara pada wilayah yang telah direklamasi secara berkala.",
		"",
		"Pasal 4",
		"(1) Pemegang izin wajib menempatkan jaminan reklamasi dalam bentuk deposito berjangka pada bank pemerintah yang ditunjuk.",
		"(2) Besaran jaminan reklamasi ditetapkan berdasarkan perhitungan biaya reklamasi yang tercantum dalam dokumen rencana reklamasi yang telah disetujui.",
		"",
		"Pasal 5",
		"(1) Pemantauan keberhasilan reklamasi dilakukan oleh tim yang dibentuk oleh instansi yang berwenang di bidang pertambangan.",
		"(2) Hasil pemantauan dituangkan dalam berita acara penilaian keberhasilan reklamasi yang disusun oleh tim pemantau.",
		"",
		"Pasal 6",
		"(1) Pemegang izin wajib menyusun rencana pascatambang paling lambat 2 (dua) tahun sebelum berakhirnya kegiatan operasi produksi.",
		"(2) Rencana pascatambang sekurang-kurangnya memuat program penataan lahan, revegetasi, dan pengembangan sosial ekonomi masyarakat sekitar wilayah pertambangan.",
	}

	currBody := []string{
		"BAB I",
		"KETENTUAN UMUM",
		"",
		"Pasal 1",
		"Peraturan ini mengatur tata cara pelaksanaan reklamasi dan pascatambang pada wilayah izin usaha pertambangan mineral dan batubara sesuai dengan rencana yang telah disetujui.",
		"",
		"BAB II",
		"TAHAPAN REKLAMASI",
		"",
		"Pasal 2",
		// ayat (2) deleted -> gap: (1) then (3).
		"(1) Pemegang izin wajib menyusun rencana reklamasi sebagai bagian dari dokumen RKAB paling lambat pada tahun pertama kegiatan operasi produksi.",
		"(3) Pelaksanaan reklamasi dilakukan secara bertahap sesuai dengan kemajuan tambang dan dilaporkan setiap semester kepada instansi yang berwenang.",
		"",
		"Pasal 3",
		"Kegiatan reklamasi sebagaimana dimaksud dalam Pasal 2 meliputi:",
		// huruf b deleted -> gap: a. then c.
		"a. penataan kembali permukaan lahan bekas tambang sesuai dengan peruntukan akhir yang direncanakan;",
		"c. pemantauan kualitas air dan udara pada wilayah yang telah direklamasi secara berkala.",
		"",
		"Pasal 4",
		// (2) mistakenly re-typed as (1) -> duplicate.
		"(1) Pemegang izin wajib menempatkan jaminan reklamasi dalam bentuk deposito berjangka pada bank pemerintah yang ditunjuk.",
		"(1) Besaran jaminan reklamasi ditetapkan berdasarkan perhitungan biaya reklamasi yang tercantum dalam dokumen rencana reklamasi yang telah disetujui.",
		"",
		"Pasal 5",
		// physically reordered -> out of order (and, since the first item is no
		// longer "(1)", bad-start fires too — both are reported, deliberately).
		"(2) Hasil pemantauan dituangkan dalam berita acara penilaian keberhasilan reklamasi yang disusun oleh tim pemantau.",
		"(1) Pemantauan keberhasilan reklamasi dilakukan oleh tim yang dibentuk oleh instansi yang berwenang di bidang pertambangan.",
		"",
		"Pasal 6",
		// both shifted up by one -> bad start alone (2,3 instead of 1,2).
		"(2) Pemegang izin wajib menyusun rencana pascatambang paling lambat 2 (dua) tahun sebelum berakhirnya kegiatan operasi produksi.",
		"(3) Rencana pascatambang sekurang-kurangnya memuat program penataan lahan, revegetasi, dan pengembangan sosial ekonomi masyarakat sekitar wilayah pertambangan.",
	}

	// Both sides run BAB I..II, so the shared tail continues at BAB III.
	tail := renumberChapter(renumberHeadings(idTail, "Pasal ", 7), 3)
	prev := append(append([]string{}, prevBody...), tail...)
	curr := append(append([]string{}, currBody...), tail...)

	return scenario{
		name: "renumbering",
		desc: "Cacat penomoran murni, satu per Pasal: gap ayat (Pasal 2), gap huruf (Pasal 3), duplikat (Pasal 4), urutan terbalik + awal salah (Pasal 5), awal salah (Pasal 6).",
		prev: prev,
		curr: curr,
	}
}

// --- 3. broken_reference ----------------------------------------------------

func brokenReferenceScenario() scenario {
	prevBody := []string{
		"BAB I",
		"KETENTUAN UMUM",
		"",
		"Pasal 1",
		"Peraturan ini mengatur kewajiban pelaporan bagi pemegang izin usaha pertambangan kepada instansi yang berwenang di bidang mineral dan batubara.",
		"",
		"BAB II",
		"TATA CARA PERIZINAN",
		"",
		"Pasal 2",
		// Deliberately, permanently unresolved: no Pasal 99 exists in either
		// version, so this is a pre-existing defect, not a regression.
		"Tata cara permohonan perpanjangan izin yang belum diatur dalam Peraturan ini mengikuti ketentuan sebagaimana dimaksud dalam Pasal 99.",
		"",
		"Pasal 3",
		"Pemegang izin wajib menyampaikan laporan pelaksanaan kegiatan penambangan secara berkala kepada instansi yang berwenang, mencakup realisasi produksi, realisasi penjualan, dan realisasi anggaran biaya, paling lambat setiap tanggal 10 pada bulan berikutnya, dalam bentuk laporan tertulis yang ditandatangani oleh kepala teknik tambang.",
		"",
		"Pasal 4",
		"Kewajiban penyampaian laporan sebagaimana diatur dalam Pasal 3 wajib dipenuhi paling lambat setiap tanggal 10 pada bulan berikutnya, dan keterlambatan penyampaian dikenakan teguran tertulis oleh instansi yang berwenang.",
		"",
		"Pasal 5",
		"Setiap kecelakaan kerja ringan yang tidak mengakibatkan hilangnya hari kerja wajib dicatat dalam buku catatan kecelakaan kerja yang disimpan di lokasi tambang dan dilaporkan pada rekapitulasi bulanan.",
		"",
		"Pasal 6",
		"Instansi yang berwenang berhak melakukan pemeriksaan lapangan sewaktu-waktu untuk memverifikasi kebenaran laporan yang disampaikan oleh pemegang izin sebagaimana dimaksud dalam Peraturan ini.",
	}

	currBody := []string{
		"BAB I",
		"KETENTUAN UMUM",
		"",
		"Pasal 1",
		"Peraturan ini mengatur kewajiban pelaporan bagi pemegang izin usaha pertambangan kepada instansi yang berwenang di bidang mineral dan batubara.",
		"",
		"BAB II",
		"TATA CARA PERIZINAN",
		"",
		"Pasal 2",
		"Tata cara permohonan perpanjangan izin yang belum diatur dalam Peraturan ini mengikuti ketentuan sebagaimana dimaksud dalam Pasal 99.",
		"",
		// Pasal 3 removed entirely — its content does not reappear anywhere else,
		// so this is a genuine deletion, not a relocation.
		"Pasal 4",
		// Still cites the now-deleted Pasal 3: this reference existed and
		// resolved in prev, and is dangling in curr. Left un-renumbered on
		// purpose — this is the common real-world mistake (delete an article,
		// forget to fix everything pointing at it).
		"Kewajiban penyampaian laporan sebagaimana diatur dalam Pasal 3 wajib dipenuhi paling lambat setiap tanggal 10 pada bulan berikutnya, dan keterlambatan penyampaian dikenakan teguran tertulis oleh instansi yang berwenang.",
		"",
		// Pasal 5 removed entirely and nobody cites it — this must surface only
		// as a structural removal, not as a broken reference.
		"Pasal 6",
		"Instansi yang berwenang berhak melakukan pemeriksaan lapangan sewaktu-waktu untuk memverifikasi kebenaran laporan yang disampaikan oleh pemegang izin sebagaimana dimaksud dalam Peraturan ini.",
	}

	// Both sides run BAB I..II, so the shared tail continues at BAB III.
	tail := renumberChapter(renumberHeadings(idTail, "Pasal ", 7), 3)
	prev := append(append([]string{}, prevBody...), tail...)
	curr := append(append([]string{}, currBody...), tail...)

	return scenario{
		name: "broken_reference",
		desc: "Pasal 3 dihapus dan masih dirujuk Pasal 4 (kritis, tanpa relokasi). Pasal 5 dihapus tanpa dirujuk siapa pun (section_removed saja). Pasal 2 merujuk Pasal 99 yang tidak pernah ada di kedua versi (minor, bukan regresi).",
		prev: prev,
		curr: curr,
	}
}

// --- 4. relocated_reference --------------------------------------------------

func relocatedReferenceScenario() scenario {
	prevBody := []string{
		"BAB I",
		"KETENTUAN UMUM",
		"",
		"Pasal 1",
		"Peraturan ini mengatur prosedur tanggap darurat lingkungan pada kegiatan operasi produksi pertambangan mineral dan batubara.",
		"",
		"Pasal 2",
		"Instansi yang berwenang menetapkan kriteria kondisi darurat lingkungan berdasarkan tingkat dampak terhadap kesehatan masyarakat dan kelestarian fungsi lingkungan hidup di sekitar wilayah pertambangan.",
		"",
		"Pasal 3",
		"Dalam hal terjadi kondisi darurat lingkungan sebagaimana ditetapkan oleh instansi yang berwenang, pemegang izin wajib menghentikan sementara seluruh kegiatan operasional pada area terdampak, melakukan pelaporan kepada instansi terkait paling lambat 1 (satu) jam setelah kejadian, dan melaksanakan langkah pemulihan sesuai dengan prosedur tanggap darurat yang telah disetujui sebelumnya.",
		"",
		"Pasal 4",
		"Pelaksanaan tanggap darurat sebagaimana diatur dalam Pasal 3 menjadi tanggung jawab penuh kepala teknik tambang yang ditunjuk secara tertulis oleh pemegang izin.",
		"",
		"Pasal 5",
		"Pemegang izin wajib melakukan simulasi tanggap darurat lingkungan sekurang-kurangnya 1 (satu) kali dalam setahun dan mendokumentasikan hasilnya sebagai bagian dari laporan pengelolaan lingkungan.",
	}

	currBody := []string{
		"BAB I",
		"KETENTUAN UMUM",
		"",
		"Pasal 1",
		"Peraturan ini mengatur prosedur tanggap darurat lingkungan pada kegiatan operasi produksi pertambangan mineral dan batubara.",
		"",
		"Pasal 2",
		"Instansi yang berwenang menetapkan kriteria kondisi darurat lingkungan berdasarkan tingkat dampak terhadap kesehatan masyarakat dan kelestarian fungsi lingkungan hidup di sekitar wilayah pertambangan.",
		"",
		// Pasal 3's number is dropped entirely (not reused) — the relocation
		// matcher only fires when a reference's target number no longer
		// resolves to anything. If a new, unrelated Pasal 3 occupied this
		// slot instead, the old "Pasal 3" citation below would still resolve
		// (to the wrong content) and never be flagged as broken at all.
		//
		// The old Pasal 3 content reappears here, at Pasal 4 — near-identical
		// wording, which is exactly what the relocation matcher looks for.
		"Pasal 4",
		"Dalam hal terjadi kondisi darurat lingkungan sebagaimana ditetapkan oleh instansi yang berwenang, pemegang izin wajib menghentikan sementara seluruh kegiatan operasional pada area terdampak, melakukan pelaporan kepada instansi terkait paling lambat 1 (satu) jam setelah kejadian, dan melaksanakan langkah pemulihan sesuai dengan prosedur tanggap darurat yang telah disetujui sebelumnya.",
		"",
		// The old citer, now at Pasal 5, still says "Pasal 3" — stale after the
		// move, and should be proposed as a repoint to Pasal 4.
		"Pasal 5",
		"Pelaksanaan tanggap darurat sebagaimana diatur dalam Pasal 3 menjadi tanggung jawab penuh kepala teknik tambang yang ditunjuk secara tertulis oleh pemegang izin.",
		"",
		"Pasal 6",
		"Pemegang izin wajib melakukan simulasi tanggap darurat lingkungan sekurang-kurangnya 1 (satu) kali dalam setahun dan mendokumentasikan hasilnya sebagai bagian dari laporan pengelolaan lingkungan.",
	}

	// Both sides run only BAB I, so the shared tail continues at BAB II.
	prev := append(append([]string{}, prevBody...), renumberChapter(renumberHeadings(idTail, "Pasal ", 6), 2)...)
	curr := append(append([]string{}, currBody...), renumberChapter(renumberHeadings(idTail, "Pasal ", 7), 2)...)

	return scenario{
		name: "relocated_reference",
		desc: "Isi Pasal 3 (prosedur tanggap darurat) pindah ke Pasal 4 tanpa referensi di Pasal 5 (dahulu Pasal 4) ikut diperbarui. Mesin mendeteksi relokasi lewat kemiripan isi dan mengusulkan repoint ke Pasal 4, bukan sekadar 'hilang'.",
		prev: prev,
		curr: curr,
	}
}

// --- 5. contract_style (English, decimal clauses) ----------------------------

func contractStyleScenario() scenario {
	prevBody := []string{
		"Article 1",
		"Definitions",
		"",
		"1.1 In this Agreement, \"Mining Area\" means the area described in Schedule 1 over which the Contractor holds mining rights granted by the relevant authority.",
		"1.2 \"Effective Date\" means the date on which this Agreement is signed by both parties and becomes binding on them.",
		"1.3 \"Force Majeure\" means any event beyond the reasonable control of a party, including natural disaster, civil unrest, or a change in applicable law materially affecting performance of this Agreement.",
		"",
		"Article 2",
		"Term",
		"",
		"2.1 This Agreement shall commence on the Effective Date and continue for an initial term of five years unless earlier terminated in accordance with this Agreement.",
		"2.2 Either party may request an extension of the term by giving written notice to the other party not less than six months before the expiry of the then-current term.",
		"2.3 Any extension agreed under Clause 2.2 shall be recorded in a written amendment signed by both parties before the expiry date.",
		"",
		"Article 3",
		"Payment",
		"",
		"3.1 The Contractor shall submit an invoice for services rendered on a monthly basis, and payment shall be made within thirty days of receipt of a valid invoice.",
		"",
		"Article 4",
		"Notices and Miscellaneous",
		"",
		"4.1 Any extension agreed under Clause 2.2 shall not affect the payment terms set out in Clause 3.1, which shall continue to apply throughout the extended term.",
		"4.2 All notices under this Agreement shall be in writing and delivered to the addresses set out in the preamble to this Agreement.",
	}

	currBody := []string{
		"Article 1",
		"Definitions",
		"",
		"1.1 In this Agreement, \"Mining Area\" means the area described in Schedule 1 over which the Contractor holds mining rights granted by the relevant authority.",
		"1.2 \"Effective Date\" means the date on which this Agreement is signed by both parties and becomes binding on them.",
		"1.3 \"Force Majeure\" means any event beyond the reasonable control of a party, including natural disaster, civil unrest, or a change in applicable law materially affecting performance of this Agreement.",
		"",
		"Article 2",
		"Term",
		"",
		"2.1 This Agreement shall commence on the Effective Date and continue for an initial term of five years unless earlier terminated in accordance with this Agreement.",
		// 2.2 deleted entirely -> clause gap, and BOTH 2.3's own text and
		// Clause 4.1 (which still cite "Clause 2.2") become dangling.
		"2.3 Any extension agreed under Clause 2.2 shall be recorded in a written amendment signed by both parties before the expiry date.",
		"",
		"Article 3",
		"Payment",
		"",
		// Copy-paste leftover: this clause is physically under Article 3 but
		// still carries a "2.x" prefix -> decimal-prefix mismatch.
		"2.4 The Contractor shall submit an invoice for services rendered on a monthly basis, and payment shall be made within thirty days of receipt of a valid invoice.",
		"",
		"Article 4",
		"Notices and Miscellaneous",
		"",
		// Still cites "Clause 3.1", which no longer exists (it is now "2.4") —
		// a second, independent broken reference from the same edit.
		"4.1 Any extension agreed under Clause 2.2 shall not affect the payment terms set out in Clause 3.1, which shall continue to apply throughout the extended term.",
		"4.2 All notices under this Agreement shall be in writing and delivered to the addresses set out in the preamble to this Agreement.",
	}

	tail := renumberArticleClauses(enTail, 5)
	prev := append(append([]string{}, prevBody...), tail...)
	curr := append(append([]string{}, currBody...), tail...)

	return scenario{
		name: "contract_style",
		desc: "Gaya kontrak berbahasa Inggris dengan penomoran desimal. Clause 2.2 dihapus (gap + dua referensi rusak), Clause 3.1 salah ketik jadi 2.4 di bawah Article 3 (decimal-prefix mismatch + referensi rusak tambahan).",
		prev: prev,
		curr: curr,
	}
}

// --- 6. kitchen_sink: a realistic amendment combining several defects -------

func kitchenSinkScenario() scenario {
	prevBody := []string{
		"BAB I",
		"KETENTUAN UMUM",
		"",
		"Pasal 1",
		"Dalam Perjanjian Kerja Sama Operasional Pertambangan ini yang dimaksud dengan Pihak Pertama adalah pemegang Izin Usaha Pertambangan Operasi Produksi, dan Pihak Kedua adalah kontraktor pelaksana kegiatan penambangan.",
		"",
		"BAB II",
		"RUANG LINGKUP KERJA SAMA",
		"",
		"Pasal 2",
		"Ruang lingkup kerja sama meliputi kegiatan pengupasan lapisan tanah penutup, penambangan, pengangkutan, dan penimbunan hasil tambang sesuai dengan RKAB yang telah disetujui oleh Pihak Pertama.",
		"",
		"Pasal 3",
		"(1) Pihak Kedua wajib menyediakan tenaga kerja, peralatan, dan bahan yang diperlukan untuk pelaksanaan kegiatan penambangan sesuai dengan RKAB yang telah disetujui.",
		"(2) Seluruh peralatan sebagaimana dimaksud pada ayat (1) harus memenuhi standar keselamatan dan kesehatan kerja pertambangan yang berlaku.",
		"(3) Pihak Kedua wajib melaporkan kesiapan peralatan sebagaimana dimaksud pada ayat (1) kepada Pihak Pertama paling lambat 7 (tujuh) hari sebelum mobilisasi.",
		"",
		"BAB III",
		"JANGKA WAKTU DAN PEMBAYARAN",
		"",
		"Pasal 4",
		"(1) Evaluasi kinerja Pihak Kedua dilakukan oleh Pihak Pertama setiap 6 (enam) bulan sekali sejak tanggal dimulainya pekerjaan sebagaimana tercantum dalam berita acara mobilisasi.",
		"(2) Hasil evaluasi sebagaimana dimaksud pada ayat (1) menjadi dasar bagi Pihak Pertama untuk memberikan teguran, perbaikan kinerja, atau pengakhiran perjanjian.",
		"",
		"Pasal 5",
		"Pembayaran imbalan jasa dilakukan setiap bulan berdasarkan volume produksi yang diverifikasi bersama oleh perwakilan kedua belah pihak di lokasi tambang.",
		"",
		"BAB IV",
		"KETENTUAN LAPORAN DAN PEMERIKSAAN",
		"",
		"Pasal 6",
		"Pemeriksaan lapangan berkala dilakukan oleh tim audit independen yang ditunjuk bersama oleh kedua belah pihak sekurang-kurangnya 1 (satu) kali dalam 3 (tiga) bulan, dengan hasil pemeriksaan dituangkan dalam laporan tertulis yang ditandatangani oleh kedua perwakilan.",
		"",
		"Pasal 7",
		"Pemeriksaan sebagaimana diatur dalam Pasal 6 dilaksanakan oleh tim independen dan hasilnya menjadi bagian dari dokumen evaluasi kinerja tahunan Pihak Kedua.",
		"",
		"BAB V",
		"KEADAAN MEMAKSA",
		"",
		"Pasal 8",
		"Force majeure adalah peristiwa di luar kemampuan wajar para pihak, termasuk bencana alam, kerusuhan, dan perubahan kebijakan pemerintah yang berdampak langsung pada pelaksanaan pekerjaan.",
		"",
		"BAB VI",
		"PENYELESAIAN PERSELISIHAN",
		"",
		"Pasal 9",
		"Setiap perselisihan yang timbul dari pelaksanaan perjanjian ini akan diselesaikan terlebih dahulu secara musyawarah untuk mufakat oleh kedua belah pihak.",
	}

	currBody := []string{
		"BAB I",
		"KETENTUAN UMUM",
		"",
		"Pasal 1",
		"Dalam Perjanjian Kerja Sama Operasional Pertambangan ini yang dimaksud dengan Pihak Pertama adalah pemegang Izin Usaha Pertambangan Operasi Produksi, dan Pihak Kedua adalah kontraktor pelaksana kegiatan penambangan.",
		"",
		"BAB II",
		"RUANG LINGKUP KERJA SAMA",
		"",
		"Pasal 2",
		"Ruang lingkup kerja sama meliputi kegiatan pengupasan lapisan tanah penutup, penambangan, pengangkutan, dan penimbunan hasil tambang sesuai dengan RKAB yang telah disetujui oleh Pihak Pertama.",
		"",
		"Pasal 3",
		// ayat (2) deleted -> numbering gap.
		"(1) Pihak Kedua wajib menyediakan tenaga kerja, peralatan, dan bahan yang diperlukan untuk pelaksanaan kegiatan penambangan sesuai dengan RKAB yang telah disetujui.",
		"(3) Pihak Kedua wajib melaporkan kesiapan peralatan sebagaimana dimaksud pada ayat (1) kepada Pihak Pertama paling lambat 7 (tujuh) hari sebelum mobilisasi.",
		"",
		"BAB III",
		"JANGKA WAKTU DAN PEMBAYARAN",
		"",
		"Pasal 4",
		// Substantive change: evaluation period doubled — a real term change.
		"(1) Evaluasi kinerja Pihak Kedua dilakukan oleh Pihak Pertama setiap 12 (dua belas) bulan sekali sejak tanggal dimulainya pekerjaan sebagaimana tercantum dalam berita acara mobilisasi.",
		"(2) Hasil evaluasi sebagaimana dimaksud pada ayat (1) menjadi dasar bagi Pihak Pertama untuk memberikan teguran, perbaikan kinerja, atau pengakhiran perjanjian.",
		"",
		"Pasal 5",
		"Pembayaran imbalan jasa dilakukan setiap bulan berdasarkan volume produksi yang diverifikasi bersama oleh perwakilan kedua belah pihak di lokasi tambang.",
		"",
		"BAB IV",
		"KETENTUAN LAPORAN DAN PEMERIKSAAN",
		"",
		// Pasal 6 removed entirely — genuinely deleted, no similar content
		// survives elsewhere. Left un-renumbered, as a real sloppy edit would.
		"Pasal 7",
		// Still cites the now-deleted Pasal 6 — dangling, critical.
		"Pemeriksaan sebagaimana diatur dalam Pasal 6 dilaksanakan oleh tim independen dan hasilnya menjadi bagian dari dokumen evaluasi kinerja tahunan Pihak Kedua.",
		"",
		"BAB V",
		"KEADAAN MEMAKSA",
		"",
		"Pasal 8",
		"Force majeure adalah peristiwa di luar kemampuan wajar para pihak, termasuk bencana alam, kerusuhan, dan perubahan kebijakan pemerintah yang berdampak langsung pada pelaksanaan pekerjaan.",
		"",
		"BAB VI",
		"PENYELESAIAN PERSELISIHAN",
		"",
		"Pasal 9",
		"Setiap perselisihan yang timbul dari pelaksanaan perjanjian ini akan diselesaikan terlebih dahulu secara musyawarah untuk mufakat oleh kedua belah pihak.",
	}

	// Both sides run BAB I..VI, so the shared tail continues at BAB VII.
	tail := renumberChapter(renumberHeadings(idTail, "Pasal ", 10), 7)
	prev := append(append([]string{}, prevBody...), tail...)
	curr := append(append([]string{}, currBody...), tail...)

	return scenario{
		name: "kitchen_sink",
		desc: "Amendemen realistis yang menggabungkan tiga jenis cacat sekaligus: perubahan teks substantif (Pasal 4), gap penomoran ayat (Pasal 3), dan referensi rusak dari Pasal 6 yang dihapus (dirujuk Pasal 7).",
		prev: prev,
		curr: curr,
	}
}
