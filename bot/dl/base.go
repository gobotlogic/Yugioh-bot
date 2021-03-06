package dl

import (
	"context"
	"errors"
	"fmt"
	"image"
	"path"
	"runtime/debug"
	"time"

	"github.com/emirpasic/gods/sets/hashset"
	"github.com/otiai10/gosseract"
	"github.com/patrickmn/go-cache"
	"github.com/sirupsen/logrus"
	"github.com/spf13/afero"
	"github.com/will7200/Yugioh-bot/bot/base"
	"gocv.io/x/gocv"
)

var (
	log       = base.CheckWithSourcedLog().With("package", "bot.base")
	providers = map[string]NewProvider{}
	client    = gosseract.NewClient()
)

// Options
type Options struct {
	Path     string
	HomeDir  string
	IsRemote bool

	Provider   Provider
	Predefined *Predefined
	FileSystem afero.Fs
	Dispatcher *base.Dispatcher
	ImageCache *cache.Cache
}

// OpenUIAsset
func OpenUIAsset(name, home string, appfs afero.Fs) (afero.File, error) {
	f, err := appfs.Open(GetUIPath(name, home))
	return f, err
}

// GetUIPath
func GetUIPath(name, home string) string {
	if path.IsAbs(name) {
		return name
	}
	return path.Join(home, "assets", name)
}

// GetImageFromAsset
func GetImageFromAsset(asset AssetMap, options Options) (gocv.Mat, error) {
	original, err := OpenUIAsset(asset.Name, options.HomeDir, options.FileSystem)
	if err != nil {
		return gocv.Mat{}, err
	}
	b, err := afero.ReadAll(original)
	original.Close()
	if err != nil {
		return gocv.Mat{}, err
	}
	imgMat, err := gocv.IMDecode(b, gocv.IMReadGrayScale)
	if imgMat.Empty() || err != nil {
		return imgMat, fmt.Errorf("Matrix is empty for resource %s", asset.Name)
	}
	return imgMat, nil
}

// TryImageFromCache
// Warning do not close the images from the cache, they will be closed on eviction
// If you need to modify, clone it.
func TryImageFromCache(asset AssetMap, options Options, c *cache.Cache) (gocv.Mat, error) {
	if img, found := c.Get(asset.Key); found {
		return img.(gocv.Mat), nil
	}
	img, err := GetImageFromAsset(asset, options)
	if err != nil {
		return img, err
	}
	c.Add(asset.Key, img, cache.DefaultExpiration)
	return img, nil
}

// NewProvider
type NewProvider func(options *Options) Provider

func RegisterProvider(name string, value NewProvider) {
	providers[name] = value
}

func GetProvider(name string, options *Options) Provider {
	if val, ok := providers[name]; ok {
		return val(options)
	}
	panic(fmt.Sprintf("Could not Get Provider %s", name))
	return nil
}

// PreCheckError
type PreCheckError struct {
	Reason error
}

// Error
func (e *PreCheckError) Error() string {
	return fmt.Sprintf("Precheck error. Reason: %s", e.Reason.Error())
}

// GenericWaitFor returns error only when returnError is specified
func GenericWaitFor(ctx context.Context, provider Provider, messeage string, checkCondition func(interface{}) bool,
	fn func(map[string]interface{}) interface{}, args map[string]interface{}) (bool, error) {
	log.Debug("Waiting for " + messeage)
	// timeout := GetDefault(args, "timeout", 10).(int)
	// returnError := GetDefault(args, "throw", true).(bool)
	tryAmount, ok := ctx.Value("tryAmount").(int)
	if !ok {
		tryAmount = 5
	}
	onPanicWait, ok := ctx.Value("panicWait").(time.Duration)
	if !ok {
		onPanicWait = time.Second * 1
	}
	onFalseCondition, ok := ctx.Value("onFalseCondition").(time.Duration)
	if !ok {
		onFalseCondition = time.Second * 2
	}
	attempts := 0
	set := hashset.New()
	errorSet := hashset.New()
	for {
		select {
		case <-ctx.Done():
			for index, val := range set.Values() {
				log.Debugf("Unique error %d", index)
				fmt.Println(val)
			}
			return false, nil
		default:
			if attempts >= tryAmount {
				for index, val := range set.Values() {
					log.Debugf("Unique error %d", index)
					fmt.Println(val)
				}
				return false, errors.New("Exceeded amount retries")
			}
			f := func() (con interface{}, failed bool) {
				defer func() {
					if r := recover(); r != nil {
						log.Debugf("Recovered in function for %s", messeage)
						switch v := r.(type) {
						case *logrus.Entry:
							if !errorSet.Contains(v.Message) {
								errorSet.Add(v.Message)
								if base.CheckIfDebug() {
									set.Add(string(debug.Stack()))
								}
							}
						}
						failed = true
						provider.WaitForUi(onPanicWait)
					}
				}()
				con = fn(args)
				return
			}
			condition, failed := f()
			attempts++
			if !failed && checkCondition(condition) {
				return true, nil
			}
			if !failed {
				provider.WaitForUi(onFalseCondition)
			}
		}
	}

}

// GetImage
func GetImage(key string, matType gocv.IMReadFlag, options Options) (*gocv.Mat, error) {
	var asset AssetMap
	asset = options.Provider.GetAsset(key)
	if asset.Key == "" {
		log.Info(options.Predefined.rbt.Keys())
		return nil, fmt.Errorf("Asset resource %s does not have a mapping", key)
	}
	original, err := OpenUIAsset(asset.Name, options.HomeDir, options.FileSystem)
	if err != nil {
		return nil, err
	}
	b, err := afero.ReadAll(original)
	original.Close()
	if err != nil {
		return nil, err
	}
	imgMat, err := gocv.IMDecode(b, matType)
	if imgMat.Empty() || err != nil {
		imgMat.Close()
		return nil, fmt.Errorf("Matrix is empty for resource %s", key)
	}
	return &imgMat, nil

}

// IsStartScreen checks whether the image is the landing page of Yugioh duel links
// The comparision of the image is compared against the image registered as start screen
// under the AssetMap region. Accomplished through SSIM, defined in Bot Constants.
func IsStartScreen(img gocv.Mat, options Options) (bool, error) {
	imgMat, err := GetImage("start_screen", gocv.IMReadGrayScale, options)
	if err != nil {
		return false, err
	}
	if !base.CVEqualDim(*imgMat, img) {
		return false, errors.New("Cannot compare two images that are not the same dimensions")
	}
	grayedMat := base.CvtColor(img, gocv.ColorBGRToGray)

	if gocv.CountNonZero(grayedMat) == 0 {
		grayedMat.Close()
		imgMat.Close()
		return false, nil
	}

	lb := base.NewMatSCScalar(140)
	ub := base.NewMatSCScalar(255)
	maskedMat := base.MaskImage(grayedMat, lb, ub, true)
	maskedOriginal := base.MaskImage(*imgMat, lb, ub, true)

	imgMat.Close()
	grayedMat.Close()

	lb.Close()
	ub.Close()

	defer maskedOriginal.Close()
	defer maskedMat.Close()
	score := base.SSIM_GOCV(maskedMat, maskedOriginal)
	log.Debugf("Start Screen Similarity: %.2f vs %.2f", score, options.Predefined.BotConst.StartScreenSimilarity)
	if score > options.Predefined.BotConst.StartScreenSimilarity {
		return true, nil
	}
	return false, nil
}

// CheckIfBattle Will Determine if the image is a precusor to a battle
// It accomplishes this by looking for the white dialog at the bottom of the screen
// The bounds are defined in the data file.
func CheckIfBattle(img gocv.Mat, percentage float64, options Options) (bool, error) {

	var asset AreaLocation
	asset = options.Provider.GetAreaLocation("battle_mode_area")
	topCorner := asset.Bounds.Lower
	bottomCorner := asset.Bounds.Upper

	if bottomCorner == (image.Point{}) {
		return false, errors.New("invalid bottom point")
	}
	// use image.Rect in case data is wrong and the lower and upper bounds are switched
	roi := img.Region(image.Rect(topCorner.X, topCorner.Y, bottomCorner.X, bottomCorner.Y))
	whiteMin := gocv.NewScalar(250, 250, 250, 0)
	whiteMax := gocv.NewScalar(255, 255, 255, 255)

	whiteQuery := gocv.NewMat()
	gocv.InRangeWithScalar(roi, whiteMin, whiteMax, &whiteQuery)

	defer roi.Close()
	defer whiteQuery.Close()

	if gocv.CountNonZero(whiteQuery) > int(float64(roi.Rows()*roi.Cols())*percentage) {
		return true, nil
	}
	return false, nil
}

// ImgToString will convert an image to a string, the image should already be masked
// to the area of focus
func ImgToString(img gocv.Mat, charSet string) (string, error) {
	if img.Empty() {
		return "", errors.New("Image is empty")
	}
	client.SetWhitelist(charSet)
	buffer, err := gocv.IMEncode(".tiff", img)
	if err != nil {
		return "", err
	}
	if img.Empty() {
		return "", errors.New("Image is empty")
	}
	client.SetImageFromBytes(buffer)
	text, err := client.Text()
	return text, err
}

// ClientImgToString uses the provided client instead of making a new one
func ClientImgToString(client gosseract.Client, img gocv.Mat) (string, error) {
	buffer, err := gocv.IMEncode(".tiff", img)
	if err != nil {
		return "", err
	}
	client.SetImageFromBytes(buffer)
	text, err := client.Text()
	return text, err
}

// CropImage
func CropImage(img gocv.Mat, key string, options Options) (*gocv.Mat, error) {
	if img.Empty() {
		return nil, errors.New("image cannot be empty")
	}
	area := options.Provider.GetAreaLocation(key)
	if err := checkArea(area); err != nil {
		return nil, err
	}
	topCorner := area.Bounds.Lower
	bottomCorner := area.Bounds.Upper
	roi := img.Region(image.Rect(topCorner.X, topCorner.Y, bottomCorner.X, bottomCorner.Y))
	return &roi, nil
}

// CenterUIArea
func CenterUIArea(key string, options Options) (*image.Point, error) {
	area := options.Provider.GetAreaLocation(key)
	if err := checkArea(area); err != nil {
		return nil, err
	}
	topCorner := area.Bounds.Lower
	bottomCorner := area.Bounds.Upper
	rect := image.Rect(topCorner.X, topCorner.Y, bottomCorner.X, bottomCorner.Y)
	t := image.Pt(rect.Min.X+rect.Dx()/2, rect.Min.Y+rect.Dy()/2)
	return &t, nil
}

// CenterUIArea
func CenterUIAreaLocation(area AreaLocation) (*image.Point, error) {
	if err := checkArea(area); err != nil {
		return nil, err
	}
	topCorner := area.Bounds.Lower
	bottomCorner := area.Bounds.Upper
	rect := image.Rect(topCorner.X, topCorner.Y, bottomCorner.X, bottomCorner.Y)
	t := image.Pt(rect.Min.X+rect.Dx()/2, rect.Min.Y+rect.Dy()/2)
	return &t, nil
}

func checkArea(area AreaLocation) (err error) {
	if area == (AreaLocation{}) {
		err = errors.New("unknown Area Location")
	}
	bottomCorner := area.Bounds.Upper

	if bottomCorner == (image.Point{}) {
		err = errors.New("invalid bottom point")
	}
	return
}
