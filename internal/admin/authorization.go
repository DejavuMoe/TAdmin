package admin

func IsOwner(ownerUserID, userID int64) bool {
	return ownerUserID > 0 && userID == ownerUserID
}
