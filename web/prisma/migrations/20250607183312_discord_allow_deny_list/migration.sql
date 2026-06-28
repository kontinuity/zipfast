/*
  Warnings:

  - You are about to drop the column `oauthDiscordWhitelistIds` on the `Zipline` table. All the data in the column will be lost.

*/
-- AlterTable
ALTER TABLE "Zipline" DROP COLUMN "oauthDiscordWhitelistIds",
ADD COLUMN     "oauthDiscordAllowedIds" TEXT[] DEFAULT ARRAY[]::TEXT[],
ADD COLUMN     "oauthDiscordDeniedIds" TEXT[] DEFAULT ARRAY[]::TEXT[];
