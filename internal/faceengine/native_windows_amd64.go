//go:build windows && amd64 && cgo

package faceengine

/*
#cgo CFLAGS: -std=c11
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef int32_t HResult;
typedef void* HFSession;
typedef void* HFImageStream;

typedef struct {
    uint8_t* data;
    int32_t width;
    int32_t height;
    int32_t format;
    int32_t rotation;
} HFImageData;

typedef struct {
    int32_t size;
    void* data;
} HFFaceBasicToken;

typedef struct {
    float* roll;
    float* yaw;
    float* pitch;
} HFFaceEulerAngle;

typedef struct {
    int32_t detectedNum;
    void* rects;
    int32_t* trackIds;
    int32_t* trackCounts;
    float* detConfidence;
    HFFaceEulerAngle angles;
    HFFaceBasicToken* tokens;
} HFMultipleFaceData;

typedef struct {
    int32_t size;
    float* data;
} HFFaceFeature;

typedef struct {
    uint32_t structSize;
    uint32_t structVersion;
    uint64_t featureMask;
    int32_t detectMode;
    int32_t maxDetectFaceNum;
    int32_t detectPixelLevel;
    int32_t trackByDetectModeFPS;
    uint32_t reserved[8];
} HFSessionConfigV2;

typedef HResult (__cdecl *fnLaunch)(const char*);
typedef HResult (__cdecl *fnTerminate)(void);
typedef HResult (__cdecl *fnCreateSessionV2)(const HFSessionConfigV2*, HFSession*);
typedef HResult (__cdecl *fnReleaseSession)(HFSession);
typedef HResult (__cdecl *fnCreateStream)(HFImageData*, HFImageStream*);
typedef HResult (__cdecl *fnReleaseStream)(HFImageStream);
typedef HResult (__cdecl *fnTrack)(HFSession, HFImageStream, HFMultipleFaceData*);
typedef HResult (__cdecl *fnGetFeatureLength)(int32_t*);
typedef HResult (__cdecl *fnExtractFeature)(HFSession, HFImageStream, HFFaceBasicToken, float*);
typedef HResult (__cdecl *fnCompare)(HFFaceFeature, HFFaceFeature, float*);
typedef HResult (__cdecl *fnConvert)(float, float*);

typedef struct {
    HMODULE module;
    HFSession session;
    fnLaunch launch;
    fnTerminate terminate;
    fnCreateSessionV2 createSession;
    fnReleaseSession releaseSession;
    fnCreateStream createStream;
    fnReleaseStream releaseStream;
    fnTrack track;
    fnGetFeatureLength getFeatureLength;
    fnExtractFeature extractFeature;
    fnCompare compare;
    fnConvert convert;
    int32_t featureLength;
} IFEngine;

typedef struct {
    float cosine;
    float confidence;
    float referenceDetectionConfidence;
    float probeDetectionConfidence;
} IFComparison;

static void if_error(char* out, int cap, const char* operation, int code) {
    if (!out || cap <= 0) return;
    if (code == INT32_MIN) {
        _snprintf_s(out, (size_t)cap, _TRUNCATE, "%s", operation);
    } else {
        _snprintf_s(out, (size_t)cap, _TRUNCATE, "%s (status=%d)", operation, code);
    }
}

static FARPROC if_symbol(HMODULE module, const char* name, char* err, int errcap) {
    FARPROC symbol = GetProcAddress(module, name);
    if (!symbol) {
        char message[160];
        _snprintf_s(message, sizeof(message), _TRUNCATE, "missing native symbol %s", name);
        if_error(err, errcap, message, INT32_MIN);
    }
    return symbol;
}

static IFEngine* if_engine_open(const char* dllPath, const char* packPath, int detectPixelSize, char* err, int errcap) {
    IFEngine* engine = (IFEngine*)calloc(1, sizeof(IFEngine));
    if (!engine) {
        if_error(err, errcap, "allocate native face engine", INT32_MIN);
        return NULL;
    }
    engine->module = LoadLibraryA(dllPath);
    if (!engine->module) {
        char message[256];
        _snprintf_s(message, sizeof(message), _TRUNCATE, "load face engine DLL failed (win32=%lu)", GetLastError());
        if_error(err, errcap, message, INT32_MIN);
        free(engine);
        return NULL;
    }

#define IF_LOAD(field, type, name) do { engine->field = (type)if_symbol(engine->module, name, err, errcap); if (!engine->field) goto fail; } while (0)
    IF_LOAD(launch, fnLaunch, "HFLaunchInspireFace");
    IF_LOAD(terminate, fnTerminate, "HFTerminateInspireFace");
    IF_LOAD(createSession, fnCreateSessionV2, "HFCreateInspireFaceSessionV2");
    IF_LOAD(releaseSession, fnReleaseSession, "HFReleaseInspireFaceSession");
    IF_LOAD(createStream, fnCreateStream, "HFCreateImageStream");
    IF_LOAD(releaseStream, fnReleaseStream, "HFReleaseImageStream");
    IF_LOAD(track, fnTrack, "HFExecuteFaceTrack");
    IF_LOAD(getFeatureLength, fnGetFeatureLength, "HFGetFeatureLength");
    IF_LOAD(extractFeature, fnExtractFeature, "HFFaceFeatureExtractCpy");
    IF_LOAD(compare, fnCompare, "HFFaceComparison");
    IF_LOAD(convert, fnConvert, "HFCosineSimilarityConvertToPercentage");
#undef IF_LOAD

    HResult status = engine->launch(packPath);
    if (status != 0 && status != 254) {
        if_error(err, errcap, "load face model pack", status);
        goto fail;
    }

    HFSessionConfigV2 config;
    memset(&config, 0, sizeof(config));
    config.structSize = (uint32_t)sizeof(config);
    config.structVersion = 1;
    config.featureMask = 0x00000002ULL;
    config.detectMode = 0;
    config.maxDetectFaceNum = 1;
    config.detectPixelLevel = detectPixelSize;
    config.trackByDetectModeFPS = -1;
    status = engine->createSession(&config, &engine->session);
    if (status != 0 || !engine->session) {
        if_error(err, errcap, "create face recognition session", status);
        goto fail_launched;
    }
    status = engine->getFeatureLength(&engine->featureLength);
    if (status != 0 || engine->featureLength <= 0 || engine->featureLength > 8192) {
        if_error(err, errcap, "query face feature length", status);
        goto fail_session;
    }
    return engine;

fail_session:
    engine->releaseSession(engine->session);
    engine->session = NULL;
fail_launched:
    engine->terminate();
fail:
    FreeLibrary(engine->module);
    free(engine);
    return NULL;
}

static int if_extract(IFEngine* engine, uint8_t* pixels, int width, int height, float* feature, float* detectionConfidence, char* err, int errcap) {
    HFImageData image;
    image.data = pixels;
    image.width = width;
    image.height = height;
    image.format = 1; // HF_STREAM_BGR
    image.rotation = 0;
    HFImageStream stream = NULL;
    HResult status = engine->createStream(&image, &stream);
    if (status != 0 || !stream) {
        if_error(err, errcap, "create face image stream", status);
        return 0;
    }
    HFMultipleFaceData faces;
    memset(&faces, 0, sizeof(faces));
    status = engine->track(engine->session, stream, &faces);
    if (status != 0) {
        if_error(err, errcap, "detect face", status);
        engine->releaseStream(stream);
        return 0;
    }
    if (faces.detectedNum < 1 || !faces.tokens) {
        if_error(err, errcap, "no face detected", INT32_MIN);
        engine->releaseStream(stream);
        return 0;
    }
    if (detectionConfidence) {
        *detectionConfidence = faces.detConfidence ? faces.detConfidence[0] : 0.0f;
    }
    HFFaceBasicToken token = faces.tokens[0];
    status = engine->extractFeature(engine->session, stream, token, feature);
    engine->releaseStream(stream);
    if (status != 0) {
        if_error(err, errcap, "extract face feature", status);
        return 0;
    }
    return 1;
}

static int if_engine_compare(IFEngine* engine,
                             uint8_t* reference, int referenceWidth, int referenceHeight,
                             uint8_t* probe, int probeWidth, int probeHeight,
                             IFComparison* result, char* err, int errcap) {
    if (!engine || !engine->session || !reference || !probe || !result) {
        if_error(err, errcap, "invalid face comparison arguments", INT32_MIN);
        return 0;
    }
    float* referenceFeature = (float*)calloc((size_t)engine->featureLength, sizeof(float));
    float* probeFeature = (float*)calloc((size_t)engine->featureLength, sizeof(float));
    if (!referenceFeature || !probeFeature) {
        if_error(err, errcap, "allocate face feature buffers", INT32_MIN);
        free(referenceFeature);
        free(probeFeature);
        return 0;
    }
    memset(result, 0, sizeof(*result));
    if (!if_extract(engine, reference, referenceWidth, referenceHeight, referenceFeature,
                    &result->referenceDetectionConfidence, err, errcap) ||
        !if_extract(engine, probe, probeWidth, probeHeight, probeFeature,
                    &result->probeDetectionConfidence, err, errcap)) {
        free(referenceFeature);
        free(probeFeature);
        return 0;
    }
    HFFaceFeature first = {engine->featureLength, referenceFeature};
    HFFaceFeature second = {engine->featureLength, probeFeature};
    HResult status = engine->compare(first, second, &result->cosine);
    free(referenceFeature);
    free(probeFeature);
    if (status != 0) {
        if_error(err, errcap, "compare face features", status);
        return 0;
    }
    status = engine->convert(result->cosine, &result->confidence);
    if (status != 0) {
        if_error(err, errcap, "convert face similarity", status);
        return 0;
    }
    result->confidence *= 100.0f;
    return 1;
}

static void if_engine_close(IFEngine* engine) {
    if (!engine) return;
    if (engine->session && engine->releaseSession) engine->releaseSession(engine->session);
    if (engine->terminate) engine->terminate();
    if (engine->module) FreeLibrary(engine->module);
    free(engine);
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"unsafe"
)

type nativeEngine struct {
	mu       sync.Mutex
	handle   *C.IFEngine
	packName string
}

func New(options Options) (Analyzer, error) {
	options, err := options.validate()
	if err != nil {
		return nil, err
	}
	dllPath := C.CString(options.LibraryPath)
	packPath := C.CString(options.ResourcePack)
	defer C.free(unsafe.Pointer(dllPath))
	defer C.free(unsafe.Pointer(packPath))
	errorBuffer := make([]byte, 512)
	handle := C.if_engine_open(
		dllPath,
		packPath,
		C.int(options.DetectPixelSize),
		(*C.char)(unsafe.Pointer(&errorBuffer[0])),
		C.int(len(errorBuffer)),
	)
	if handle == nil {
		return nil, errors.New(cString(errorBuffer))
	}
	return &nativeEngine{handle: handle, packName: filepath.Base(options.ResourcePack)}, nil
}

func (e *nativeEngine) Name() string {
	if e == nil {
		return "local-mnn"
	}
	return "local-mnn/" + e.packName
}

func (e *nativeEngine) Compare(ctx context.Context, referenceEncoded, probeEncoded []byte) (Comparison, error) {
	if err := ctx.Err(); err != nil {
		return Comparison{}, err
	}
	reference, err := decodeBGR(referenceEncoded)
	if err != nil {
		return Comparison{}, fmt.Errorf("reference image: %w", err)
	}
	probe, err := decodeBGR(probeEncoded)
	if err != nil {
		return Comparison{}, fmt.Errorf("probe image: %w", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.handle == nil {
		return Comparison{}, errors.New("local face engine is closed")
	}
	errorBuffer := make([]byte, 512)
	var result C.IFComparison
	ok := C.if_engine_compare(
		e.handle,
		(*C.uint8_t)(unsafe.Pointer(&reference.Pixels[0])), C.int(reference.Width), C.int(reference.Height),
		(*C.uint8_t)(unsafe.Pointer(&probe.Pixels[0])), C.int(probe.Width), C.int(probe.Height),
		&result,
		(*C.char)(unsafe.Pointer(&errorBuffer[0])), C.int(len(errorBuffer)),
	)
	if ok == 0 {
		return Comparison{}, errors.New(cString(errorBuffer))
	}
	if err := ctx.Err(); err != nil {
		return Comparison{}, err
	}
	return Comparison{
		Cosine:                       float64(result.cosine),
		Confidence:                   float64(result.confidence),
		ReferenceDetectionConfidence: float64(result.referenceDetectionConfidence),
		ProbeDetectionConfidence:     float64(result.probeDetectionConfidence),
	}, nil
}

func (e *nativeEngine) Close() error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.handle != nil {
		C.if_engine_close(e.handle)
		e.handle = nil
	}
	return nil
}

func cString(buffer []byte) string {
	for index, value := range buffer {
		if value == 0 {
			if index == 0 {
				return "local face engine failed"
			}
			return string(buffer[:index])
		}
	}
	return string(buffer)
}
