import whatsapp


def test_chats_can_skip_the_last_message(db):
    chat = whatsapp.get_chat("5511@s.whatsapp.net", include_last_message=False)
    assert (chat.name, chat.last_message) == ("João", None)
    chats = whatsapp.list_chats(include_last_message=False)
    assert [(c.name, c.last_message) for c in chats] == [("João", None)]
