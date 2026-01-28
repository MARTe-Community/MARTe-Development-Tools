MDT=../../build/mdt

all: check build

check:
	$(MDT) check src/*.marte

build:
	$(MDT) build -o app_full.marte src/*.marte

fmt:
	$(MDT) fmt src/*.marte
