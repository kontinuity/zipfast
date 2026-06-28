-- AlterTable
ALTER TABLE "Zipline" ADD COLUMN     "oauthDiscordWhitelistIds" TEXT[] DEFAULT ARRAY[]::TEXT[];
