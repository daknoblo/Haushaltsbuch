/** @type {import('tailwindcss').Config} */
module.exports = {
  // Tailwind matches bare words anywhere in a file, so scanning all of
  // internal/web would let ordinary prose in a Go comment emit a utility and
  // break the "CSS is up to date" check on an unrelated edit. Only the
  // templates and viewmodel.go, the single Go file that builds class strings,
  // are scanned.
  content: [
    "./internal/web/**/*.templ",
    "./internal/web/viewmodel.go",
  ],
  // The palette is dark by default; app.js toggles the class on <html>.
  darkMode: "class",
  theme: {
    extend: {},
  },
  plugins: [],
};
