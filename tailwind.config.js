/** @type {import('tailwindcss').Config} */
module.exports = {
  content: [
    "./internal/web/**/*.templ",
    "./internal/web/**/*.go",
  ],
  // The palette is dark by default; app.js toggles the class on <html>.
  darkMode: "class",
  theme: {
    extend: {},
  },
  plugins: [],
};
