# diff-checker

Pembanding dokumen legal: menemukan perubahan teks, cacat penomoran, dan
referensi silang yang rusak antara dua versi dokumen.

Dirancang untuk dokumen hukum Indonesia — hierarki BAB / Bagian / Paragraf /
Pasal / ayat / huruf / angka sesuai kaidah UU 12/2011 — dan untuk kontrak
bergaya penomoran desimal (`Article 1`, `1.1`, `1.1.2`).

## Kenapa bukan sekadar "compare pakai AI"

Microsoft Word sudah bisa membandingkan teks. Yang tidak bisa dilakukan Word
adalah memberi tahu bahwa **menyisipkan satu Pasal membuat referensi silang di
bawahnya salah**. Itulah masalah yang dikerjakan proyek ini.

Prinsip desainnya: **deterministik dulu, LLM belakangan.**

| Pekerjaan | Cara |
|---|---|
| Parse heading, marker, hierarki | Go — regex + state machine |
| Cari lokasi perubahan teks | Go — Myers diff |
| Highlight kata yang berubah | Go — word-level diff |
| Validasi urutan penomoran | Go — walk marker sequence |
| Deteksi broken cross-reference | Go — grammar referensi + resolve ke tree |
| Klasifikasi makna & risiko legal | LLM (belum diimplementasi) |

Konsekuensinya, setiap temuan diberi kelas yang tampil jelas:

- **`verified`** — hasil algoritma deterministik. Reproducible, tidak bisa
  halusinasi, nol biaya token.
- **`advisory`** — hasil LLM. Ditandai eksplisit sebagai saran.

## Status

Mesin deterministik sudah berjalan penuh dan **tidak membutuhkan API key**.
Lapisan agentic dan UI web belum ada.

| Komponen | Status |
|---|---|
| Ingest DOCX (termasuk tabel & tracked changes) | ✅ |
| Ingest PDF (via `pdftotext`) dan teks/Markdown | ✅ |
| Parser struktur + referensi silang | ✅ |
| Word-level diff + highlight | ✅ |
| Validator penomoran | ✅ |
| Validator integritas referensi | ✅ |
| CLI `diffctl` | ✅ |
| Lapisan agentic (trpc-agent-go) | ⬜ |
| UI web + SSE | ⬜ |

## Menjalankan

```bash
make build          # -> bin/diffctl, bin/server
make test           # seluruh test
make check          # yang dijalankan CI: fmt, vet, test, verify
make demo           # bandingkan pasangan dokumen contoh
```

### Membandingkan dua dokumen

```bash
diffctl compare sebelum.docx sesudah.docx
diffctl compare sebelum.docx sesudah.docx --changes   # + daftar perubahan teks
diffctl compare sebelum.docx sesudah.docx --json      # laporan JSON
```

Status keluar bisa dipakai sebagai gerbang CI:
`0` bersih, `1` ada temuan mayor (dengan `--fail-on-major`), `2` ada temuan kritis.

### Memeriksa struktur satu dokumen

```bash
diffctl inspect dokumen.docx          # cetak pohon struktur
diffctl inspect dokumen.docx --refs   # + seluruh referensi silang
```

## Contoh keluaran

Pada pasangan contoh di `testdata/pair01/` — di mana Pasal 3 dihapus, huruf b
hilang, dan ayat (3) hilang:

```
Ringkasan perubahan
  ditambah   : 0
  dihapus    : 0
  diubah     : 2
  total      : 2
  jumlah pasal: 4 -> 3

Temuan (5 terverifikasi, 0 saran AI)
  kritis: 1  mayor: 4  minor: 0  info: 0

  [CRITICAL] Paragraf [13] merujuk ke Pasal 3, yang dihapus di versi baru —
             referensi menjadi menggantung  (broken_reference)
  [MAJOR]    huruf b hilang dalam Pasal 2 ayat (2) — melompat dari a ke c
             usul (renumber): [12] "c." -> "b."
  [MAJOR]    ayat 3 hilang dalam Pasal 2 — melompat dari 2 ke 4
  [MAJOR]    Pasal 3 hilang — melompat dari 2 ke 4
  [MAJOR]    Pasal 3 tidak lagi ada di versi baru
```

## Format yang didukung

| Format | Catatan |
|---|---|
| `.docx` | Paragraf termasuk isi tabel. Tracked changes diselesaikan ke tampilan *accepted*. |
| `.pdf` | Butuh `pdftotext` (paket `poppler-utils`). PDF hasil scan ditolak dengan pesan jelas — OCR di luar cakupan v1. |
| `.txt`, `.md` | Satu baris = satu paragraf. |

## Arsitektur

```
internal/
  docmodel/    tipe inti: Paragraph, Node, Marker, Reference, Finding
  ingest/      DOCX / PDF / teks -> IndexedDoc ber-indeks [N]
  structure/   heading, marker, pohon hierarki, ekstraksi referensi
  textdiff/    word-level diff + highlight <b>
  rules/       validator penomoran & integritas referensi (deterministik)
  report/      perakitan laporan + renderer teks
cmd/
  diffctl/     CLI
  server/      web server (masih kerangka)
```

Skema pengalamatan `[N]` per paragraf konsisten di seluruh tahap: setiap temuan,
referensi, dan usulan aksi menunjuk paragraf dengan indeks yang sama.

## Lisensi

MIT — lihat [LICENSE](LICENSE).
