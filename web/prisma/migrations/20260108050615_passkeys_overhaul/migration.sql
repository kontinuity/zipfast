/*
  Warnings:

  - You are about to drop the column `mfaPasskeys` on the `Zipline` table. All the data in the column will be lost.

*/
-- AlterTable
ALTER TABLE "public"."Zipline" DROP COLUMN "mfaPasskeys",
ADD COLUMN     "mfaPasskeysEnabled" BOOLEAN NOT NULL DEFAULT false,
ADD COLUMN     "mfaPasskeysOrigin" TEXT,
ADD COLUMN     "mfaPasskeysRpID" TEXT;
