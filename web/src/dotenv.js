// --require ./src/dotenv.js
// loads environment variables from a .env file into process.env on startup
try {
  process.loadEnvFile('.env');
} catch {}
