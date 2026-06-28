-- AlterTable
ALTER TABLE "Zipline" ADD COLUMN     "featuresVersionAPI" TEXT NOT NULL DEFAULT 'https://zipline-version.diced.sh',
ADD COLUMN     "featuresVersionChecking" BOOLEAN NOT NULL DEFAULT true;
