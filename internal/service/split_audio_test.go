package service

import (
	"context"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestGetSplitPointsRejectsInvalidSegmentDurationBeforeProbingInput(t *testing.T) {
	for _, duration := range []float64{0, MIN_SEGMENT_DURATION - 0.1, math.NaN(), math.Inf(1)} {
		if _, err := GetSplitPoints("input-is-not-read", duration); err == nil {
			t.Fatalf("GetSplitPoints() error = nil for segment duration %v", duration)
		}
	}
}

func TestValidateAudioDurationRejectsNonPositiveAndNonFiniteValues(t *testing.T) {
	for _, duration := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if err := validateAudioDuration(duration); err == nil {
			t.Fatalf("validateAudioDuration() error = nil for duration %v", duration)
		}
	}
	if err := validateAudioDuration(0.001); err != nil {
		t.Fatalf("validateAudioDuration() error = %v for positive finite duration", err)
	}
}

func TestFinalizeSplitPointsKeepsSingleShortSegmentStart(t *testing.T) {
	got := finalizeSplitPoints([]float64{0, 0}, 1, 2.0)
	want := []float64{0, 2.0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("finalizeSplitPoints() = %v, want %v", got, want)
	}
}

func TestFinalizeSplitPointsMergesShortTailWhenPreviousSegmentExists(t *testing.T) {
	got := finalizeSplitPoints([]float64{0, 300, 0}, 2, 305.0)
	want := []float64{0, 305.0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("finalizeSplitPoints() = %v, want %v", got, want)
	}
}

func TestProcessAudioSegmentsRejectsSingleTimePoint(t *testing.T) {
	_, err := (Service{}).processAudioSegments(context.Background(), nil, []float64{2.0})
	if err == nil {
		t.Fatal("processAudioSegments() error = nil, want invalid split-point error")
	}
	if !strings.Contains(err.Error(), "at least two time points") {
		t.Fatalf("processAudioSegments() error = %q, want time-point validation error", err)
	}
}

func TestProcessAudioSegmentsRejectsNonIncreasingTimePoints(t *testing.T) {
	_, err := (Service{}).processAudioSegments(context.Background(), nil, []float64{0, 5, 5})
	if err == nil {
		t.Fatal("processAudioSegments() error = nil, want non-increasing split-point error")
	}
	if !strings.Contains(err.Error(), "strictly increasing") {
		t.Fatalf("processAudioSegments() error = %q, want ordering validation error", err)
	}
}

func TestProcessAudioSegmentsRejectsInvalidTimePointValues(t *testing.T) {
	for _, points := range [][]float64{{-1, 2}, {0, math.NaN()}, {0, math.Inf(1)}} {
		_, err := (Service{}).processAudioSegments(context.Background(), nil, points)
		if err == nil {
			t.Fatalf("processAudioSegments() error = nil for points %v", points)
		}
		if !strings.Contains(err.Error(), "finite and non-negative") {
			t.Fatalf("processAudioSegments() error = %q, want finite-value validation error", err)
		}
	}
}
