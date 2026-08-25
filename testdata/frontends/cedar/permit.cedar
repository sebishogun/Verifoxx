permit(
  principal == User::"alice",
  action == Action::"read",
  resource == Document::"report"
) when { context.team == "blue" && context.enabled };
