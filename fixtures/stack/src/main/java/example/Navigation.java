package example;

public class Navigation {
    public String lombokGetter(Person person) {
        return person.getName();
    }
    public Person lombokBuilder() {
        return Person.builder().name("Alice").build();
    }
    public PersonDto mapping(Person person) {
        return new PersonMapperImpl().toDto(person);
    }
    public String querydsl() {
        return QPerson.person.name.toString();
    }
}
