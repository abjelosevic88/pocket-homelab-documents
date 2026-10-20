# Demo archive for the screenshots

All names, numbers and companies are made up. Run from an empty working folder:

```sh
python3 scripts/demo/gen.py            # src/*.html
# render with Gotenberg (any container on its network), e.g. for each file:
#   curl -F "files=@src/x.html;filename=index.html" -F printBackground=true http://gotenberg:3000/forms/chromium/convert/html -o pdf/x.pdf
# three of them are turned into phone photos (pdftoppm + a slight tilt, paper tone and noise with Pillow):
#   2025-12-15_godisnji_porez, nalaz_dom_zdravlja, obavjestenje_odvoz_otpada → .jpg
docker run -d --name pd-shots -p 127.0.0.1:18190:8080 -e ADMIN_EMAIL=ana@example.com -e ADMIN_PASSWORD=screenshots-2026 \
  -e "ADMIN_NAME=Ana Petrović" -e OCR_LANGUAGE=eng+srp_latn+srp+hrv ghcr.io/abjelosevic88/pocket-homelab-documents
python3 scripts/demo/seed.py           # labels, rules, uploads; wait until Processing is empty
python3 scripts/demo/seed2.py          # fields, notes, views, folder, mail rules
```
