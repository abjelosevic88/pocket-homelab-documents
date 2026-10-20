# Screenshots

Taken from a demo instance with made-up documents: `scripts/screenshots.mjs` drives a headless Chrome over the DevTools protocol (see the comment at its top). The demo archive is built from HTML rendered to PDF by Gotenberg; three of them are turned into phone photos so OCR has something real to do. To retake them, start the demo on :18190, seed it, start `zenika/alpine-chrome`, and run `EMAIL=… PASSWORD=… node scripts/screenshots.mjs` (`SHOTS=home,document` for some).
