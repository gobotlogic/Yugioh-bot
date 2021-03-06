package dl

import (
	"fmt"
	"image"
	"image/color"
	"time"

	cluster2 "github.com/cdipaolo/goml/cluster"
	"github.com/patrickmn/go-cache"
	"github.com/will7200/Yugioh-bot/bot/base"
	"gocv.io/x/gocv"
	"gocv.io/x/gocv/contrib"
)

var (
	blue          = color.RGBA{0, 0, 255, 0}
	red           = color.RGBA{255, 0, 0, 0}
	debugDetector = base.CheckIfDebug()
)

// Correlation
type Correlation int

// detector
type detector struct {
	predefined *Predefined
	cache      *cache.Cache
	options    *Options
}

// Compare
func (d *detector) Compare(key string, img gocv.Mat, correlation Correlation) (bool, image.Point) {
	asset := d.predefined.GetAsset(AssetPrefix + key)
	if asset.Key == "" {
		log.Panic(fmt.Sprintf("Detector %s does not exist", key))
	}
	trainImage, err := TryImageFromCache(asset, *d.options, d.cache)
	if err != nil {
		log.Panic(err)
	}
	if trainImage.Empty() || img.Empty() {
		log.Panic("image is empty")
	}
	var scaleFactor float64
	scaleFactor = asset.ScaleFactor
	if asset.ScaleFactor == 0 {
		scaleFactor = .80
	}
	sift := contrib.NewSIFT()
	defer sift.Close()
	mask := gocv.NewMat()

	kp1, des1 := sift.DetectAndCompute(trainImage, mask)
	kp2, des2 := sift.DetectAndCompute(img, mask)

	defer des1.Close()
	defer des2.Close()

	if des1.Empty() || des2.Empty() {
		return false, image.Pt(0, 0)
	}
	bf := gocv.NewBFMatcher()
	defer bf.Close()
	matches := bf.KnnMatch(des1, des2, 2)
	goodMatches := make([]gocv.DMatch, 0, len(matches))
	cluster := make([][]float64, 0, len(matches))
	for i := range matches {
		m, n := matches[i][0], matches[i][1]
		kpQuery := kp1[m.QueryIdx]
		kpTrain := kp2[m.TrainIdx]
		_, p2 := base.PointFromKeyPoint(kpQuery), base.PointFromKeyPoint(kpTrain)
		if len(asset.Bounds) == 0 {
			if m.Distance < scaleFactor*n.Distance && p2.Y > asset.YThres && p2.X < asset.XThres {
				goodMatches = append(goodMatches, m)
				cluster = append(cluster, []float64{kpTrain.X, kpTrain.Y})
			}
		} else if len(asset.Bounds) == 1 && (asset.Bounds[0].Upper == image.Point{}) {
			if m.Distance < scaleFactor*n.Distance && p2.Y > asset.Bounds[0].Lower.Y && p2.X < asset.Bounds[0].Lower.X {
				goodMatches = append(goodMatches, m)
				cluster = append(cluster, []float64{kpTrain.X, kpTrain.Y})
			}
		} else {
			log.Panic("Multiple bounds do not work right now")
		}
	}
	log.Debugf("SIFT run for %s correlation %d: %d, %t", asset.Description, correlation, len(cluster), len(cluster) > int(correlation))
	if len(cluster) < int(correlation) {
		return false, image.Pt(0, 0)
	}

	log.Debug("Running clustering algo")
	model := cluster2.NewKMeans(1, 300, cluster)

	if model.Learn() != nil {
		log.Panic("Could not train model")
	}
	return true, image.Pt(int(model.Centroids[0][0]), int(model.Centroids[0][1]))
}

// Circles
func (d *detector) Circles(key string, img gocv.Mat) ([]Circle, bool) {
	asset := d.predefined.GetAsset(AssetPrefix + key)
	if asset.Key == "" {
		log.Panic(fmt.Sprintf("Detector %s does not exist", key))
	}
	hcParams := d.predefined.BotConst.CirclesDefinitions.HoughCirclesDefinitions
	lowerB := d.predefined.BotConst.CirclesDefinitions.LowerBound
	upperB := d.predefined.BotConst.CirclesDefinitions.UpperBound

	lb := gocv.NewScalar(lowerB, lowerB, lowerB, 0)
	ub := gocv.NewScalar(upperB, upperB, upperB, 255)

	whiteQuery := gocv.NewMat()
	circles := gocv.NewMat()

	gocv.InRangeWithScalar(img, lb, ub, &whiteQuery)
	gocv.HoughCirclesWithParams(whiteQuery, &circles, gocv.HoughGradient,
		hcParams.DP,
		hcParams.MinDist,
		hcParams.Param1,
		hcParams.Param2,
		hcParams.MinRadius,
		hcParams.MaxRadius)
	whiteQuery.Close()

	aCircles := make([]Circle, circles.Cols())
	cimg := img.Clone()
	for i := 0; i < circles.Cols(); i++ {
		v := circles.GetVecfAt(0, i)
		x := int(v[0])
		y := int(v[1])
		r := int(v[2])
		aCircles = append(aCircles, Circle{image.Pt(x, y), r})

		if debugDetector {
			gocv.Circle(&cimg, image.Pt(x, y), r, blue, 2)
			gocv.Circle(&cimg, image.Pt(x, y), 2, red, 3)
		}
	}
	if debugDetector {
		gocv.IMWrite(fmt.Sprintf("circles-%s.png", time.Now().Format(time.RFC3339)), cimg)
	}
	cimg.Close()
	circles.Close()
	return aCircles, false
}

// Detector
type Detector interface {
	Compare(key string, img gocv.Mat, correlation Correlation) (bool, image.Point)
	Circles(key string, img gocv.Mat) ([]Circle, bool)
}

// Circle
type Circle struct {
	Point  image.Point
	Radius int
}

// NewDetector
func NewDetector(options *Options) Detector {
	return &detector{
		options.Predefined,
		options.ImageCache,
		options,
	}
}

// NewDetectorWithCache
func NewDetectorWithCache(options *Options, c *cache.Cache) Detector {
	return &detector{
		predefined: options.Predefined,
		cache:      c,
		options:    options,
	}
}
