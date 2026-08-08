# Fixture skenario — testdata/scenarios/

Setiap folder berisi satu pasang dokumen `prev`/`curr` dalam format `.docx`
**dan** `.pdf`, dihasilkan oleh `go run ./testdata/gen` (lihat
`testdata/gen/scenarios.go`). Semua pasangan minimal 3 halaman.

Temuan di bawah ini **bukan spekulasi** — hasil menjalankan langsung:

```
diffctl compare testdata/scenarios/<nama>/prev.docx testdata/scenarios/<nama>/curr.docx
```

DOCX dan PDF diverifikasi menghasilkan temuan struktural yang identik untuk
setiap skenario (jumlah kritis/mayor/minor sama persis di kedua format);
hanya jumlah paragraf "diubah" yang bisa sedikit berbeda karena `pdftotext`
merekonstruksi batas paragraf dari tata letak, bukan dari markup asli.

| Skenario | Halaman (prev/curr) | Kritis | Mayor | Minor |
|---|---|---|---|---|
| `text_only` | 5 / 5 | 0 | 0 | 0 |
| `renumbering` | 4 / 4 | 0 | 4 | 2 |
| `broken_reference` | 3 / 3 | 1 | 4 | 1 |
| `relocated_reference` | 3 / 3 | 1 | 1 | 1 |
| `contract_style` | 3 / 3 | 0 | 2 | 3 |
| `kitchen_sink` | 4 / 4 | 1 | 3 | 0 |

---

## text_only

Perubahan teks murni — substantif, kosmetik, dan case-only — tanpa
perubahan struktur sama sekali.

**Hasil aktual:** `Tidak ada temuan struktural.` — persis seperti yang
diharapkan; tiga baris diklasifikasikan tepat oleh `textdiff`:

| # | Lokasi | Perubahan | Klasifikasi |
|---|---|---|---|
| 10 | Pasal 5 ayat (1) | "30 hari" → "60 hari" | substantif |
| 20 | Pasal 6 | tanda baca (koma ditambahkan) | **kosmetik** |
| 30 | Pasal 7 | "Pihak Pertama" → "pihak pertama" (case-only) | substantif (bukan kosmetik) |

Ini membuktikan `isCosmetic()` benar memisahkan perubahan tanda-baca-saja
dari perubahan makna, dan bahwa perubahan case pada istilah terdefinisi
tetap dianggap substantif meski kata-katanya identik.

## renumbering

Lima cacat penomoran murni, satu per Pasal, tanpa perubahan referensi:

- **Pasal 2** — ayat (2) dihapus → gap `numbering_gap` ((1) lompat ke (3))
- **Pasal 3** — huruf b dihapus → gap `numbering_gap` (a. lompat ke c.)
- **Pasal 4** — ayat (2) diketik ulang jadi (1) → `numbering_duplicate`
- **Pasal 5** — urutan dibalik fisik → `numbering_out_of_order` **dan**
  `numbering_bad_start` (karena item pertama bukan lagi "(1)") — keduanya
  memang dilaporkan bersamaan, sesuai desain
- **Pasal 6** — kedua ayat digeser naik satu → `numbering_bad_start` saja

Semua 6 temuan yang diharapkan muncul persis, tanpa noise tambahan.

## broken_reference

- **Pasal 3** dihapus dan masih dirujuk oleh Pasal 4 →
  **[CRITICAL] broken_reference**, aksi `manual_review` (tanpa relokasi
  karena isinya benar-benar tidak ditemukan di tempat lain)
- **Pasal 5** dihapus tanpa dirujuk siapa pun → hanya `section_removed`
  (mayor), tidak dilaporkan sebagai broken reference — benar, karena tidak
  ada yang merujuknya
- **Pasal 2** merujuk **Pasal 99** yang tidak pernah ada di kedua versi →
  **[MINOR]**, ditandai eksplisit "sudah bermasalah sejak versi
  sebelumnya" — bukan regresi dari revisi ini

Tiga jenis kasus referensi (kritis-dengan-manual-review, section-removed
tanpa-referensi, dan pre-existing-minor) semuanya terbukti terklasifikasi
dengan tepat oleh `rules/refintegrity.go`.

## relocated_reference

Isi Pasal 3 (prosedur tanggap darurat) pindah ke Pasal 4; Pasal 3 sebagai
nomor **dihapus total** (tidak dipakai ulang oleh pasal lain), dan Pasal 5
(citer lama) tetap menyebut "Pasal 3".

**Hasil aktual:**

- **[CRITICAL] shifted_reference** — "Paragraf [13] merujuk ke Pasal 3,
  yang sudah tidak ada di versi baru. Isinya tampak dipindahkan ke Pasal 4"
  dengan usul aksi `reference_update`: `"Pasal 3" -> "Pasal 4"`
- `numbering_gap` (mayor) untuk lompatan penomoran 2→4
- `section_removed` **diturunkan ke MINOR** (bukan mayor) karena mesin
  mendeteksi kontennya pindah, bukan hilang murni

**Catatan desain penting (koreksi dari draf awal):** skenario ini semula
menaruh Pasal 3 yang *baru dan tidak terkait* di slot nomor 3 (nomor
dipakai ulang). Itu justru membuat referensi lama "Pasal 3" tetap
ter-*resolve* (ke konten yang salah) dan **tidak pernah** ditandai broken,
sehingga jalur deteksi relokasi tidak pernah aktif — sebuah kasus nyata
yang layak dicatat: pendeteksi referensi saat ini hanya menangkap
referensi yang gagal me-resolve sama sekali; ia tidak (dan secara desain
tidak bisa, tanpa LLM) mendeteksi "nomor exists tapi isinya sudah beda".
Skenario sudah diperbaiki (nomor 3 tidak dipakai ulang) sehingga jalur
relokasi yang sebenarnya bisa didemokan di sini.

## contract_style

Gaya kontrak berbahasa Inggris, penomoran desimal (`Article N` / `N.M`):

- **Clause 2.2** dihapus → `numbering_gap` (mayor) di Article 2, **dan**
  dua referensi ke "Clause 2.2" (dari 2.3 dan dari 4.1) ditandai
  **broken_reference minor** — minor karena, secara teknis, referensi
  desimal "2.2" tidak pernah punya node sendiri yang terdaftar sebagai
  "ada" sebelum dihapus dalam representasi saat ini (lihat batasan di
  bawah), jadi diperlakukan sebagai pre-existing, bukan regresi
- **Clause 3.1** salah ketik jadi "2.4" di bawah Article 3 →
  `numbering_bad_start` (mayor: klausul diawali 2, seharusnya 3), dan
  referensi ke "Clause 3.1" dari 4.1 juga **broken_reference minor**

**Bug mesin ditemukan lewat skenario ini (belum diperbaiki):** setiap
referensi ke klausul desimal ("Clause 2.2", "Article 3.1") selalu
dilaporkan sebagai "sudah bermasalah sejak versi sebelumnya" (minor) —
tidak pernah sebagai broken_reference kritis, bahkan ketika targetnya
benar-benar dihapus oleh revisi ini (kasus Clause 2.2 di skenario ini
seharusnya kritis). Akar masalah: `Reference.TargetID()`
(`internal/docmodel/reference.go:51`) merender ID sebagai `article:2.2`
untuk sitiran "Clause 2.2" — karena regex `reRefAbsolute` menangkap seluruh
"2.2" sebagai satu grup `Pasal`, bukan Pasal="2" + Ayat="2" — sementara ID
node pohon yang sebenarnya adalah `article:2/clause:2.2`
(`node.markerNodeKind`/`structure/build.go`, level klausul desimal
memakai kind `clause`, bukan `ayat`). `FindByCitation`
(`internal/docmodel/node.go:263`) juga tidak menangani kasus ini — ia
hanya mencoba `pasal:<pasal>` / `article:<pasal>` lalu turun lewat
ayat/huruf/angka, tidak pernah lewat `clause:`. Akibatnya `resolve()`
gagal untuk **prev maupun curr**, jadi setiap referensi desimal dianggap
"tidak pernah ada" dan otomatis jatuh ke kelas minor/pre-existing, tidak
peduli apakah benar-benar baru rusak oleh revisi ini. Ini murni bug di
`internal/rules/refintegrity.go` + `internal/docmodel/reference.go`, bukan
sesuatu yang bisa diperbaiki dari sisi fixture.

## kitchen_sink

Amendemen realistis yang menggabungkan tiga jenis cacat sekaligus:

- **Pasal 4** — evaluasi kinerja "6 bulan" → "12 bulan": perubahan teks
  substantif (tidak muncul sebagai temuan struktural, benar — itu masuk
  ranah `textdiff`/tier LLM `analyze`, bukan `rules`)
- **Pasal 3** — ayat (2) dihapus → `numbering_gap` (mayor)
- **Pasal 6** dihapus dan masih dirujuk Pasal 7 →
  **[CRITICAL] broken_reference** + `numbering_gap` (mayor, lompatan 5→7)
  + `section_removed` (mayor, karena isinya benar-benar tidak ditemukan di
  tempat lain — bukan relokasi)

Semua tiga cacat yang dijanjikan skenario muncul, tanpa temuan liar dari
BAB penutup bersama (`idTail`).

---

## Catatan implementasi generator

- `testdata/gen/idTail` / `enTail` — boilerplate penutup bersama, dipakai
  ulang di semua skenario untuk memenuhi syarat minimal 3 halaman.
  **Nomor BAB dan Pasal/Article/klausulnya di-renumber otomatis** per
  skenario (`renumberChapter`, `renumberHeadings`,
  `renumberArticleClauses` di `testdata/gen/util.go`) agar tidak
  bertabrakan dengan nomor yang sudah dipakai skenario itu sendiri — versi
  awal generator melewatkan ini dan menghasilkan temuan `numbering_gap`
  palsu ("BAB VI hilang") serta `numbering_bad_start` palsu (klausul
  "900.1" di bawah Article 5-10) di hampir semua skenario. Sudah
  diperbaiki dan diverifikasi ulang.
- Lingkungan generasi awalnya tidak punya `libreoffice-writer` terpasang
  (hanya `libreoffice-core`), sehingga `soffice --convert-to pdf` gagal
  total secara diam-diam (keluar dengan kode 0 tapi tanpa file output).
  `convertToPDF` sekarang memverifikasi file PDF benar-benar dihasilkan,
  bukan hanya mempercayai exit code.
